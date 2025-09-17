// Plugin Management Module
// Handles all plugin-related functionality including loading, toggling, upload, and online installation

// Plugin upload state management
let uploadListenersSetup = false;

// Plugin Management Functions

// Check which plugins need initialization
async function checkPluginInitializationStatus(activePluginNames) {
  const initStatus = new Map();

  try {
    // Make a test chat request to get uninitialized plugins info
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question: '_check_init_status_' })
    });

    if (response.ok) {
      const data = await response.json();
      if (data.requires_initialization && data.uninitialized_plugins) {
        for (const plugin of data.uninitialized_plugins) {
          if (activePluginNames.has(plugin.name)) {
            initStatus.set(plugin.name, {
              needsInit: true,
              configVars: plugin.required_config || [],
              isLegacy: plugin.legacy_plugin || false
            });
          }
        }
      }
    }
  } catch (error) {
    console.warn('Failed to check plugin initialization status:', error);

    // Fallback: try individual config endpoints
    for (const pluginName of activePluginNames) {
      try {
        const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/config`);
        if (response.ok) {
          const configData = await response.json();
          initStatus.set(pluginName, {
            needsInit: !configData.is_initialized,
            configVars: configData.required_config || [],
            isLegacy: false
          });
        }
      } catch (error) {
        console.warn(`Failed to check initialization status for ${pluginName}:`, error);
      }
    }
  }

  return initStatus;
}

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

    // Fetch plugin initialization status for active plugins
    const pluginInitStatus = await checkPluginInitializationStatus(activePluginNames);

    displayPlugins(localPlugins, activePluginNames, pluginInitStatus);
  } catch (error) {
    console.error('Error loading plugins:', error);
    const pluginsList = document.getElementById('pluginsList');
    if (pluginsList) {
      pluginsList.innerHTML = '<div class="text-danger small">Failed to load plugins</div>';
    }
  }
}

// Display plugins in the sidebar
function displayPlugins(plugins, activePluginNames, pluginInitStatus = new Map()) {
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
    const initStatus = pluginInitStatus.get(plugin.name);
    const needsConfig = isActive && initStatus && initStatus.needsInit;

    return `
      <div class="plugin-item">
        <div class="d-flex align-items-center justify-content-between">
          <div>
            <div class="fw-medium d-flex align-items-center" style="color: var(--text-primary);">
              ${plugin.name}
              ${isUploaded ? '<span class="badge badge-success ms-2" style="font-size: 0.7em;">Local</span>' : ''}
              ${needsConfig ? '<span class="badge badge-warning ms-2" style="font-size: 0.7em;">Setup Required</span>' : ''}
            </div>
            <div class="text-muted small">${plugin.description || 'No description available'}</div>
            ${plugin.version ? `<div class="text-muted" style="font-size: 0.7em;">v${plugin.version}</div>` : ''}
          </div>
          <div class="d-flex align-items-center">
            ${needsConfig ? `
              <button class="btn btn-sm btn-outline-warning me-2 plugin-config-btn"
                      data-plugin-name="${plugin.name}"
                      title="Configure plugin">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                </svg>
              </button>
            ` : ''}
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
        alert(`Failed to ${isEnabled ? 'enable' : 'disable'} plugin: ${error.message}`);
      }
    });
  });

  // Setup plugin configuration buttons
  const configButtons = document.querySelectorAll('.plugin-config-btn');
  configButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      const pluginName = e.target.closest('button').dataset.pluginName;
      await showPluginConfigModal(pluginName);
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
    // Check if plugin has filepath settings that need user input
    const hasFilepathSettings = await checkPluginFilepathSettings(pluginName, pluginPath);
    if (hasFilepathSettings) {
      const userSettings = await showFilepathSettingsModal(pluginName, hasFilepathSettings);
      if (!userSettings) {
        // User cancelled
        return;
      }
      // Continue with enable process using user settings
      return await enablePluginWithSettings(pluginName, pluginPath, userSettings);
    }

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
    // For disabling, unload from cache
    const unloadResponse = await fetch(`/api/plugins?name=${encodeURIComponent(pluginName)}`, {
      method: 'DELETE'
    });

    if (!unloadResponse.ok) {
      const errorText = await unloadResponse.text();
      throw new Error(`Failed to unload plugin: ${errorText}`);
    }

    console.log(`Plugin ${pluginName} disabled successfully`);

    // Refresh the plugins list to show the updated state
    await loadPlugins();
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

// Show plugin configuration modal
async function showPluginConfigModal(pluginName) {
  try {
    // Fetch plugin configuration info
    const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/config`);
    if (!response.ok) {
      throw new Error('Failed to fetch plugin configuration');
    }

    const configData = await response.json();

    // Check if this is a legacy plugin with current settings
    if (configData.is_legacy_plugin && configData.current_settings) {
      showLegacyPluginConfigModal(pluginName, configData.current_settings);
      return;
    }

    // Handle modern plugins with required_config
    const configVars = configData.required_config || [];
    if (configVars.length === 0) {
      // No configuration needed, but still allow manual setup for legacy plugins
      showLegacyPluginSetupModal(pluginName);
      return;
    }

    // Create modal HTML
    const modalHtml = `
      <div class="modal fade" id="pluginConfigModal" tabindex="-1" aria-labelledby="pluginConfigModalLabel" aria-hidden="true">
        <div class="modal-dialog">
          <div class="modal-content" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="pluginConfigModalLabel" style="color: var(--text-primary);">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                </svg>
                Configure ${pluginName}
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
            </div>
            <div class="modal-body">
              <form id="pluginConfigForm">
                <p style="color: var(--text-secondary); margin-bottom: 20px;">
                  This plugin requires configuration before it can be used. Please provide the following information:
                </p>
                ${configVars.map(configVar => `
                  <div class="mb-3">
                    <label for="config_${configVar.name}" class="form-label" style="color: var(--text-primary);">
                      ${configVar.name}
                      ${configVar.required ? '<span style="color: var(--danger-color);">*</span>' : ''}
                    </label>
                    ${configVar.type === 'password' ? `
                      <input type="password" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.description}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    ` : configVar.type === 'number' ? `
                      <input type="number" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.description}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    ` : configVar.type === 'boolean' ? `
                      <div class="form-check">
                        <input type="checkbox" class="form-check-input" id="config_${configVar.name}" name="${configVar.name}">
                        <label class="form-check-label" for="config_${configVar.name}" style="color: var(--text-secondary);">
                          ${configVar.description}
                        </label>
                      </div>
                    ` : `
                      <input type="text" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.description}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    `}
                    <div class="form-text" style="color: var(--text-secondary);">
                      ${configVar.description}
                    </div>
                  </div>
                `).join('')}
              </form>
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"
                      style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">
                Cancel
              </button>
              <button type="button" class="btn btn-primary" id="savePluginConfigBtn"
                      style="background: var(--accent-color); border-color: var(--accent-color);">
                Save Configuration
              </button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove existing modal if present
    const existingModal = document.getElementById('pluginConfigModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Add modal to page
    document.body.insertAdjacentHTML('beforeend', modalHtml);

    // Setup save button event listener
    document.getElementById('savePluginConfigBtn').addEventListener('click', async () => {
      await savePluginConfig(pluginName, configVars);
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('pluginConfigModal'));
    modal.show();

  } catch (error) {
    console.error('Error showing plugin config modal:', error);
    alert(`Failed to show plugin configuration: ${error.message}`);
  }
}

// Save plugin configuration
async function savePluginConfig(pluginName, configVars) {
  try {
    const form = document.getElementById('pluginConfigForm');
    const formData = new FormData(form);
    const configData = {};

    // Convert form data to config object
    for (const configVar of configVars) {
      const value = formData.get(configVar.name);
      if (configVar.type === 'boolean') {
        configData[configVar.name] = document.getElementById(`config_${configVar.name}`).checked;
      } else if (configVar.type === 'number') {
        configData[configVar.name] = value ? Number(value) : null;
      } else {
        configData[configVar.name] = value;
      }
    }

    // Send configuration to server
    const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/initialize`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(configData)
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save plugin configuration');
    }

    // Close modal
    const modal = bootstrap.Modal.getInstance(document.getElementById('pluginConfigModal'));
    modal.hide();

    // Show success message
    alert(`Plugin "${pluginName}" has been configured successfully!`);

    // Refresh plugins list to update status
    await loadPlugins();

  } catch (error) {
    console.error('Error saving plugin config:', error);
    alert(`Failed to save plugin configuration: ${error.message}`);
  }
}

// Show legacy plugin configuration modal with actual settings fields
async function showLegacyPluginConfigModal(pluginName, currentSettings) {
  try {
    // Dynamically generate field information from current settings
    const settingsFields = [];

    for (const [key, value] of Object.entries(currentSettings)) {
      // Skip the 'initialized' field as it's handled automatically
      if (key === 'initialized') continue;

      // Convert snake_case to human readable labels
      const label = key.split('_').map(word =>
        word.charAt(0).toUpperCase() + word.slice(1)
      ).join(' ');

      // Determine field type based on value type
      let fieldType = 'text';
      let fieldValue = value;

      if (typeof value === 'boolean') {
        fieldType = 'checkbox';
      } else if (typeof value === 'number') {
        fieldType = 'number';
      } else {
        fieldValue = String(value || '');
      }

      // Generate description based on field name
      let description = `Configure the ${label.toLowerCase()}`;
      if (key.includes('dir') || key.includes('directory')) {
        description = `Path to the ${label.toLowerCase().replace(' dir', ' directory')}`;
      } else if (key.includes('template')) {
        description = `Path to the ${label.toLowerCase()}`;
      } else if (key.includes('script')) {
        description = `Path to the ${label.toLowerCase()}`;
      }

      settingsFields.push({
        name: key,
        label: label,
        type: fieldType,
        value: fieldValue,
        description: description
      });
    }

    // Create modal HTML for legacy plugin configuration
    const modalHtml = `
      <div class="modal fade" id="pluginConfigModal" tabindex="-1" aria-labelledby="pluginConfigModalLabel" aria-hidden="true">
        <div class="modal-dialog modal-lg">
          <div class="modal-content" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="pluginConfigModalLabel" style="color: var(--text-primary);">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                </svg>
                Configure ${pluginName}
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
            </div>
            <div class="modal-body">
              <form id="pluginConfigForm">
                <p style="color: var(--text-secondary); margin-bottom: 20px;">
                  Configure the plugin settings below. These settings will be saved to your agent configuration.
                </p>
                ${settingsFields.map(field => `
                  <div class="mb-3">
                    <label for="setting_${field.name}" class="form-label" style="color: var(--text-primary);">
                      ${field.label}
                    </label>
                    ${field.type === 'checkbox' ? `
                      <div class="form-check">
                        <input type="checkbox" class="form-check-input" id="setting_${field.name}" name="${field.name}"
                               ${field.value ? 'checked' : ''}
                               style="background: var(--bg-tertiary); border: 1px solid var(--border-color);">
                        <label class="form-check-label" for="setting_${field.name}" style="color: var(--text-secondary);">
                          ${field.description}
                        </label>
                      </div>
                    ` : `
                      <input type="${field.type}" class="form-control" id="setting_${field.name}" name="${field.name}"
                             value="${field.value}"
                             placeholder="${field.description}"
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                      <div class="form-text" style="color: var(--text-secondary);">
                        ${field.description}
                      </div>
                    `}
                  </div>
                `).join('')}
              </form>
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"
                      style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">
                Cancel
              </button>
              <button type="button" class="btn btn-primary" id="saveLegacyConfigBtn"
                      style="background: var(--accent-color); border-color: var(--accent-color);">
                Save Configuration
              </button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove existing modal if present
    const existingModal = document.getElementById('pluginConfigModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Add modal to page
    document.body.insertAdjacentHTML('beforeend', modalHtml);

    // Setup save button event listener
    document.getElementById('saveLegacyConfigBtn').addEventListener('click', async () => {
      await saveLegacyPluginConfig(pluginName, settingsFields);
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('pluginConfigModal'));
    modal.show();

  } catch (error) {
    console.error('Error showing legacy plugin config modal:', error);
    alert(`Failed to show plugin configuration: ${error.message}`);
  }
}

// Prepare parameters for complete_setup operation based on plugin type
function prepareCompleteSetupParams(pluginName, configData) {
  console.log('Preparing params for plugin:', pluginName, 'with data:', configData);
  const params = ['operation="complete_setup"'];

  if (pluginName === 'music_project_manager') {
    // music_project_manager expects project_dir and template_dir
    if (configData.project_dir) {
      params.push(`project_dir="${configData.project_dir}"`);
    }
    if (configData.template_dir) {
      params.push(`template_dir="${configData.template_dir}"`);
    }
    // Add default template if provided
    if (configData.default_template) {
      params.push(`default_template="${configData.default_template}"`);
    }
  } else if (pluginName === 'reascript_launcher') {
    // reascript_launcher expects scripts_dir
    if (configData.scripts_dir) {
      params.push(`scripts_dir="${configData.scripts_dir}"`);
    }
  } else {
    // For other plugins, add all non-initialized fields as parameters
    for (const [key, value] of Object.entries(configData)) {
      if (key !== 'initialized' && value) {
        params.push(`${key.toLowerCase()}="${value}"`);
      }
    }
  }

  console.log('Generated params:', params.join(', '));
  return params.join(', ');
}

function prepareCompleteSetupParamsObject(pluginName, configData) {
  console.log('Preparing params object for plugin:', pluginName, 'with data:', configData);
  const params = { operation: "complete_setup" };

  if (pluginName === 'music_project_manager') {
    // music_project_manager expects project_dir and template_dir
    if (configData.project_dir) {
      params.project_dir = configData.project_dir;
    }
    if (configData.template_dir) {
      params.template_dir = configData.template_dir;
    }
    // Add default template if provided
    if (configData.default_template) {
      params.default_template = configData.default_template;
    }
  } else if (pluginName === 'reascript_launcher') {
    // reascript_launcher expects scripts_dir
    if (configData.scripts_dir) {
      params.scripts_dir = configData.scripts_dir;
    }
  } else {
    // For other plugins, add all non-initialized fields as parameters
    for (const [key, value] of Object.entries(configData)) {
      if (key !== 'initialized' && value) {
        params[key.toLowerCase()] = value;
      }
    }
  }

  console.log('Generated params object:', params);
  return params;
}

// Save legacy plugin configuration
async function saveLegacyPluginConfig(pluginName, settingsFields) {
  try {
    const form = document.getElementById('pluginConfigForm');
    const formData = new FormData(form);
    const configData = {};

    // Convert form data to config object
    for (const field of settingsFields) {
      if (field.type === 'checkbox') {
        configData[field.name] = document.getElementById(`setting_${field.name}`).checked;
      } else if (field.type === 'number') {
        const value = formData.get(field.name);
        configData[field.name] = value ? Number(value) : 0;
      } else {
        configData[field.name] = formData.get(field.name) || '';
      }
    }

    // Mark as initialized
    configData.initialized = true;

    // Call complete_setup operation via plugin execution
    const parameters = prepareCompleteSetupParamsObject(pluginName, configData);

    console.log('Plugin configuration parameters:', parameters);
    console.log('Config data:', configData);
    console.log('Form field names and values:');
    for (const key in configData) {
      console.log(`  ${key}: ${configData[key]}`);
    }

    const response = await fetch('/api/plugins/execute', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        plugin_name: pluginName,
        parameters: parameters
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save plugin configuration');
    }

    // Parse response to check for errors from plugin execution
    const result = await response.json();
    if (result.error) {
      throw new Error(result.error);
    }

    // Close modal
    const modal = bootstrap.Modal.getInstance(document.getElementById('pluginConfigModal'));
    modal.hide();

    // Show success message
    alert(`Plugin "${pluginName}" has been configured successfully!`);

    // Refresh plugins list to update status
    await loadPlugins();

  } catch (error) {
    console.error('Error saving legacy plugin config:', error);
    alert(`Failed to save plugin configuration: ${error.message}`);
  }
}

// Show legacy plugin setup modal for plugins that require manual configuration
async function showLegacyPluginSetupModal(pluginName) {
  try {
    // Create modal HTML for legacy plugins
    const modalHtml = `
      <div class="modal fade" id="pluginConfigModal" tabindex="-1" aria-labelledby="pluginConfigModalLabel" aria-hidden="true">
        <div class="modal-dialog">
          <div class="modal-content" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="pluginConfigModalLabel" style="color: var(--text-primary);">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2.18 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                </svg>
                Configure ${pluginName}
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
            </div>
            <div class="modal-body">
              <div style="color: var(--text-secondary); margin-bottom: 20px;">
                <p>This plugin requires manual setup through chat interaction.</p>
                <p>Click "Start Setup" below, then follow the prompts in the chat to configure the plugin.</p>
                <div class="alert alert-info" style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                  <strong>Setup Instructions:</strong><br>
                  1. Click "Start Setup" to begin<br>
                  2. The plugin will guide you through configuration in the chat<br>
                  3. Follow the prompts to set required directories and settings
                </div>
              </div>
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"
                      style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">
                Cancel
              </button>
              <button type="button" class="btn btn-primary" id="startLegacySetupBtn"
                      style="background: var(--accent-color); border-color: var(--accent-color);">
                Start Setup
              </button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove existing modal if present
    const existingModal = document.getElementById('pluginConfigModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Add modal to page
    document.body.insertAdjacentHTML('beforeend', modalHtml);

    // Setup start setup button event listener
    document.getElementById('startLegacySetupBtn').addEventListener('click', async () => {
      await startLegacyPluginSetup(pluginName);
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('pluginConfigModal'));
    modal.show();

  } catch (error) {
    console.error('Error showing legacy plugin setup modal:', error);
    alert(`Failed to show plugin setup: ${error.message}`);
  }
}

// Start legacy plugin setup by sending a setup command via chat
async function startLegacyPluginSetup(pluginName) {
  try {
    // Close the modal
    const modal = bootstrap.Modal.getInstance(document.getElementById('pluginConfigModal'));
    modal.hide();

    // Send a setup command to the chat
    const setupMessage = `${pluginName} init_setup`;

    // Add the setup message to chat and send it
    if (typeof sendMessage === 'function') {
      await sendMessage(setupMessage);
    } else {
      // Fallback: manually trigger the setup via API
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ question: setupMessage })
      });

      if (response.ok) {
        const data = await response.json();
        // Add both user message and response to chat
        if (typeof addMessageToChat === 'function') {
          addMessageToChat(setupMessage, true);
          addMessageToChat(data.response, false);
        }
      }
    }

    // Refresh plugins list after setup
    setTimeout(() => {
      loadPlugins();
    }, 1000);

  } catch (error) {
    console.error('Error starting legacy plugin setup:', error);
    alert(`Failed to start plugin setup: ${error.message}`);
  }
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

// Check if plugin has filepath settings that require user input
async function checkPluginFilepathSettings(pluginName, pluginPath) {
  try {
    // Temporarily enable plugin to call get_settings
    const tempEnableResponse = await fetch('/api/plugins', {
      method: 'POST',
      body: (() => {
        const formData = new FormData();
        formData.append('name', pluginName);
        formData.append('path', pluginPath);
        return formData;
      })()
    });

    if (!tempEnableResponse.ok) {
      return null; // Can't check, proceed normally
    }

    // Call get_settings to see what fields are needed
    const settingsResponse = await fetch('/api/plugins/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plugin_name: pluginName,
        parameters: { operation: 'get_settings' }
      })
    });

    if (settingsResponse.ok) {
      const result = await settingsResponse.json();
      if (result.result) {
        const settings = JSON.parse(result.result);
        const filepathFields = {};

        // Find fields with 'filepath' type
        for (const [fieldName, fieldType] of Object.entries(settings)) {
          if (fieldType === 'filepath') {
            filepathFields[fieldName] = fieldType;
          }
        }

        // Disable the plugin again since this was just for checking
        await fetch(`/api/plugins?name=${encodeURIComponent(pluginName)}`, {
          method: 'DELETE'
        });

        return Object.keys(filepathFields).length > 0 ? filepathFields : null;
      }
    }

    return null;
  } catch (error) {
    console.error('Error checking plugin filepath settings:', error);
    return null;
  }
}

// Show modal for filepath settings input
async function showFilepathSettingsModal(pluginName, filepathFields) {
  return new Promise((resolve) => {
    // Create modal HTML
    const modalHtml = `
      <div class="modal fade" id="filepathSettingsModal" tabindex="-1" aria-labelledby="filepathSettingsModalLabel" aria-hidden="true">
        <div class="modal-dialog modal-lg">
          <div class="modal-content" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="filepathSettingsModalLabel" style="color: var(--text-primary);">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/>
                </svg>
                Configure ${pluginName} - File Paths
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
            </div>
            <div class="modal-body">
              <form id="filepathSettingsForm">
                <p style="color: var(--text-secondary); margin-bottom: 20px;">
                  Please select the file paths for this plugin configuration:
                </p>
                ${Object.keys(filepathFields).map(fieldName => {
                  const displayName = fieldName.split('_').map(word =>
                    word.charAt(0).toUpperCase() + word.slice(1)
                  ).join(' ');

                  return `
                    <div class="mb-3">
                      <label for="filepath_${fieldName}" class="form-label" style="color: var(--text-primary);">
                        ${displayName}
                      </label>
                      <div class="input-group">
                        <input type="text" class="form-control" id="filepath_${fieldName}" name="${fieldName}"
                               placeholder="${fieldName.includes('dir') ? '/Users/username/Documents/Folder' : '/Users/username/Documents/file.txt'}"
                               style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                        <button type="button" class="btn btn-outline-secondary file-browse-btn" data-field="${fieldName}">
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
                          </svg>
                          Help
                        </button>
                      </div>
                      <div class="form-text" style="color: var(--text-secondary);">
                        ${fieldName.includes('dir') ? 'Enter the full path to the directory (folder)' : 'Enter the full path to the file'}. Paths with spaces are supported.
                      </div>
                    </div>
                  `;
                }).join('')}
              </form>
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"
                      style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">
                Cancel
              </button>
              <button type="button" class="btn btn-primary" id="saveFilepathSettingsBtn"
                      style="background: var(--accent-color); border-color: var(--accent-color);">
                Configure Plugin
              </button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove existing modal if present
    const existingModal = document.getElementById('filepathSettingsModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Add modal to page
    document.body.insertAdjacentHTML('beforeend', modalHtml);

    // Setup file browse buttons
    document.querySelectorAll('.file-browse-btn').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        const fieldName = e.target.closest('button').dataset.field;
        const inputField = document.getElementById(`filepath_${fieldName}`);
        const isDirectory = fieldName.includes('dir');

        // For web browsers, we can't actually browse the full filesystem
        // So we'll provide a better input experience with validation
        alert(`Please enter the full path for ${displayName.toLowerCase()}.\n\nExamples:\n- Directory: /Users/username/Documents/MyFolder\n- File: /Users/username/Documents/file.txt\n\nNote: You need to type the complete path as web browsers cannot browse your filesystem.`);

        // Focus the input field for easier typing
        inputField.focus();
      });
    });

    // Setup save button
    document.getElementById('saveFilepathSettingsBtn').addEventListener('click', () => {
      const form = document.getElementById('filepathSettingsForm');
      const formData = new FormData(form);
      const settings = {};

      for (const [key, value] of formData.entries()) {
        settings[key] = value;
      }

      // Validate that all fields are filled and contain valid paths
      const emptyFields = Object.keys(filepathFields).filter(field => !settings[field] || settings[field].trim() === '');
      if (emptyFields.length > 0) {
        alert(`Please fill in all required fields: ${emptyFields.join(', ')}`);
        return;
      }

      // Basic path validation - just check if it looks like a path
      const invalidFields = [];
      for (const [field, value] of Object.entries(settings)) {
        const trimmedValue = value.trim();
        // Very basic validation - just check if it starts with / or contains some path-like structure
        if (!trimmedValue.match(/^[\/~]|^[A-Za-z]:[\/\\]/) && !trimmedValue.includes('/')) {
          invalidFields.push(field);
        }
      }

      if (invalidFields.length > 0) {
        alert(`Please enter valid file paths for: ${invalidFields.join(', ')}\n\nPaths should start with / (Unix/Mac) or C:\\ (Windows) or be relative paths with forward slashes.`);
        return;
      }

      // Close modal and resolve with settings
      const modal = bootstrap.Modal.getInstance(document.getElementById('filepathSettingsModal'));
      modal.hide();
      resolve(settings);
    });

    // Setup cancel button
    document.querySelector('[data-bs-dismiss="modal"]').addEventListener('click', () => {
      resolve(null);
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('filepathSettingsModal'));
    modal.show();
  });
}

// Enable plugin with user-provided settings
async function enablePluginWithSettings(pluginName, pluginPath, userSettings) {
  try {
    // Enable the plugin first
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
      throw new Error(errorText || 'Failed to enable plugin');
    }

    // Now save the user settings
    const settingsResponse = await fetch('/api/plugins/save-settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plugin_name: pluginName,
        settings: userSettings
      })
    });

    if (!settingsResponse.ok) {
      const errorText = await settingsResponse.text();
      throw new Error(`Failed to save plugin settings: ${errorText}`);
    }

    console.log(`Plugin ${pluginName} enabled successfully with user settings`);

  } catch (error) {
    console.error('Error enabling plugin with settings:', error);
    throw error;
  }
}

// Initialize plugin management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupPluginManagement);
} else {
  setupPluginManagement();
}
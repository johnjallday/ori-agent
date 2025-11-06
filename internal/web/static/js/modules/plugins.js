// Plugin Management Module
// Handles all plugin-related functionality including loading, toggling, upload, and online installation

// Plugin upload state management
let uploadListenersSetup = false;

// Check plugin configuration status - automatically detect by checking default-settings endpoint
async function checkPluginConfigurationStatus(activePluginNames) {
  const configStatus = new Map();

  // Check each active plugin to see if it has configuration
  for (const pluginName of activePluginNames) {
    let hasConfig = false;

    try {
      // Try to fetch default-settings endpoint for this plugin
      const response = await fetch(`/api/plugins/${pluginName}/default-settings`);

      if (response.ok) {
        // Plugin has default-settings endpoint, so it's configurable
        hasConfig = true;
        console.log(`Plugin ${pluginName} has configuration (default-settings endpoint found)`);
      } else {
        console.log(`Plugin ${pluginName} has no configuration (default-settings returned ${response.status})`);
      }
    } catch (error) {
      console.log(`Plugin ${pluginName} configuration check failed:`, error);
      hasConfig = false;
    }

    configStatus.set(pluginName, {
      needsInit: false,        // For simplicity, assume plugins are initialized
      hasConfig: hasConfig,    // Show config if default-settings endpoint exists
      configVars: [],
      isLegacy: false
    });
  }

  return configStatus;
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

    // Create a set of active plugin names for quick lookup (only enabled ones)
    const activePluginNames = new Set(
      activePlugins.plugins
        .filter(p => p.enabled === true)
        .map(p => p.name)
    );

    // Filter to only show local plugins in sidebar (those without github_repo)
    const localPlugins = registry.plugins.filter(plugin => !plugin.github_repo);

    // Fetch plugin configuration status for active plugins
    console.log('About to call checkPluginConfigurationStatus with:', activePluginNames);
    let pluginConfigStatus;
    try {
      pluginConfigStatus = await checkPluginConfigurationStatus(activePluginNames);
      console.log('checkPluginConfigurationStatus returned:', pluginConfigStatus);
    } catch (error) {
      console.error('Error in checkPluginConfigurationStatus:', error);
      pluginConfigStatus = new Map(); // fallback to empty map
    }

    displayPlugins(localPlugins, activePluginNames, pluginConfigStatus);
  } catch (error) {
    console.error('Error loading plugins:', error);
    const pluginsList = document.getElementById('pluginsList');
    if (pluginsList) {
      pluginsList.innerHTML = '<div class="text-danger small">Failed to load plugins</div>';
    }
  }
}

// Display plugins in the sidebar
function displayPlugins(plugins, activePluginNames, pluginConfigStatus = new Map()) {
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
    const configStatus = pluginConfigStatus.get(plugin.name);
    const needsConfig = isActive && configStatus && configStatus.needsInit;
    const hasConfig = isActive && configStatus && configStatus.hasConfig;

    // Debug logging for config button visibility
    if (isActive) {
      console.log(`Plugin: ${plugin.name}`);
      console.log(`  - isActive: ${isActive}`);
      console.log(`  - configStatus:`, configStatus);
      console.log(`  - needsConfig: ${needsConfig}`);
      console.log(`  - hasConfig: ${hasConfig}`);
      console.log(`  - Will show config button: ${hasConfig}`);
    }


    return `
      <div class="plugin-item">
        <div class="d-flex align-items-center justify-content-between">
          <div>
            <div class="fw-medium d-flex align-items-center" style="color: var(--text-primary);">
              ${plugin.name}
              ${isUploaded ? '<span class="badge badge-success ms-2" style="font-size: 0.7em;">Local</span>' : ''}
              ${needsConfig ? '<span class="badge badge-warning ms-2" style="font-size: 0.7em;">Setup Required</span>' : ''}
            </div>
            <div class="small" style="color: var(--text-muted);">${plugin.description || 'No description available'}</div>
            ${plugin.version ? `<div style="font-size: 0.7em; color: var(--text-muted);">v${plugin.version}</div>` : ''}
          </div>
          <div class="d-flex align-items-center">
            ${hasConfig ? `
              <button class="btn btn-sm ${needsConfig ? 'btn-outline-warning' : 'btn-outline-secondary'} me-2 plugin-config-btn"
                      data-plugin-name="${plugin.name}"
                      data-plugin-path="${plugin.path}"
                      title="${needsConfig ? 'Configure plugin (setup required)' : 'Configure plugin'}">
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
  console.log('Setting up plugin config buttons');
  const configButtons = document.querySelectorAll('.plugin-config-btn');
  console.log(`Found ${configButtons.length} config buttons to set up`);
  configButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      const button = e.target.closest('button');
      const pluginName = button.dataset.pluginName;
      const pluginPath = button.dataset.pluginPath;

      console.log(`Config button clicked for plugin: ${pluginName}`);

      // All configurable plugins use the same modal
      // The modal will fetch settings from /api/plugins/{name}/default-settings
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

    let enableResponseData = null;

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

        // Get response data from retry
        try {
          enableResponseData = await retryResponse.json();
        } catch (e) {
          console.log('Failed to parse retry response as JSON');
        }
      } else {
        throw new Error(errorText || 'Failed to enable plugin');
      }
    } else {
      // Get response data from successful enable
      try {
        enableResponseData = await enableResponse.json();
      } catch (e) {
        console.log('Failed to parse enable response as JSON');
      }
    }

    console.log(`Plugin ${pluginName} enabled successfully`);

    // Check if plugin needs configuration modal
    if (enableResponseData && enableResponseData.show_config_modal === true) {
      console.log(`Plugin ${pluginName} requires configuration, showing modal...`);
      // Refresh plugins list first
      await loadPlugins();
      // Show configuration modal
      await showPluginConfigModal(pluginName);
      return; // Don't refresh again after modal
    }

    // Refresh the plugins list to show the updated state
    await loadPlugins();

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

  // Validate file size (e.g., max 50MB)
  const maxSize = 50 * 1024 * 1024; // 50MB
  if (file.size > maxSize) {
    showUploadResult('error', 'File size too large. Maximum size is 50MB.');
    return;
  }

  // Note: We accept all file types here since plugins can be:
  // - RPC executables (no extension or platform-specific)
  // - Shared libraries (.so, .dll, .dylib)
  // The backend will validate if it's actually a valid plugin

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

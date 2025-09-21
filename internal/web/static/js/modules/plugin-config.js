// Plugin Configuration Module
// Handles all plugin configuration functionality including config modals, filepath settings, and legacy setup

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
      alert(`${pluginName} is a legacy plugin. Legacy plugin configuration is no longer supported. Please use modern plugin configuration methods.`);
      return;
    }

    // Handle modern plugins with required_config
    const configVars = configData.required_config || [];
    if (configVars.length === 0) {
      // No configuration needed
      alert(`${pluginName} plugin is ready to use - no configuration required.`);
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

    // Refresh plugins list to update status - call loadPlugins from main plugins module
    if (typeof loadPlugins === 'function') {
      await loadPlugins();
    }

  } catch (error) {
    console.error('Error saving plugin config:', error);
    alert(`Failed to save plugin configuration: ${error.message}`);
  }
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
  // Try to fetch default settings for this plugin
  let defaultSettings = {};
  try {
    const defaultResponse = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/default-settings`);
    if (defaultResponse.ok) {
      const defaultData = await defaultResponse.json();
      if (defaultData.success && defaultData.default_settings) {
        defaultSettings = defaultData.default_settings;
      }
    }
  } catch (error) {
    console.warn('Failed to fetch default settings for plugin:', pluginName, error);
  }

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
                               placeholder="${defaultSettings[fieldName] || (fieldName.includes('dir') ? '/Users/username/Documents/Folder' : '/Users/username/Documents/file.txt')}"
                               style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                        <button type="button" class="btn btn-outline-secondary file-browse-btn" data-field="${fieldName}">
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
                          </svg>
                          Browse
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

        try {
          let selectedPath = '';

          if (isDirectory) {
            // Use directory picker if available
            if ('showDirectoryPicker' in window) {
              const dirHandle = await window.showDirectoryPicker();
              selectedPath = dirHandle.name; // This only gives folder name, not full path

              // Since we can't get the full path from the File System Access API,
              // we'll prompt the user to enter the full path manually
              selectedPath = prompt(
                `Selected directory: "${dirHandle.name}"\n\nPlease enter the full path to this directory:`,
                inputField.value || inputField.placeholder
              );
            } else {
              // Fallback: create a file input for directory selection
              const input = document.createElement('input');
              input.type = 'file';
              input.webkitdirectory = true;
              input.multiple = false;

              input.onchange = (event) => {
                const files = event.target.files;
                if (files.length > 0) {
                  // Get the directory path from the first file
                  const firstFile = files[0];
                  const fullPath = firstFile.webkitRelativePath;
                  const dirPath = fullPath.substring(0, fullPath.lastIndexOf('/'));

                  // Since we still can't get the absolute path, ask user to provide it
                  selectedPath = prompt(
                    `Selected directory contains: "${firstFile.name}"\n\nPlease enter the full absolute path to this directory:`,
                    inputField.value || inputField.placeholder
                  );

                  if (selectedPath) {
                    inputField.value = selectedPath;
                  }
                }
              };

              input.click();
              return; // Exit early since the callback will handle setting the value
            }
          } else {
            // Use file picker for files
            if ('showOpenFilePicker' in window) {
              const [fileHandle] = await window.showOpenFilePicker();
              selectedPath = fileHandle.name; // This only gives filename, not full path

              // Since we can't get the full path from the File System Access API,
              // we'll prompt the user to enter the full path manually
              selectedPath = prompt(
                `Selected file: "${fileHandle.name}"\n\nPlease enter the full path to this file:`,
                inputField.value || inputField.placeholder
              );
            } else {
              // Fallback: create a file input for file selection
              const input = document.createElement('input');
              input.type = 'file';
              input.multiple = false;

              input.onchange = (event) => {
                const file = event.target.files[0];
                if (file) {
                  // Since we can't get the absolute path, ask user to provide it
                  selectedPath = prompt(
                    `Selected file: "${file.name}"\n\nPlease enter the full absolute path to this file:`,
                    inputField.value || inputField.placeholder
                  );

                  if (selectedPath) {
                    inputField.value = selectedPath;
                  }
                }
              };

              input.click();
              return; // Exit early since the callback will handle setting the value
            }
          }

          if (selectedPath && selectedPath.trim() !== '') {
            inputField.value = selectedPath.trim();
          }

        } catch (error) {
          console.error('Error browsing for path:', error);

          // Fallback to manual input
          const defaultValue = defaultSettings[fieldName];
          const displayName = fieldName.split('_').map(word =>
            word.charAt(0).toUpperCase() + word.slice(1)
          ).join(' ');

          const exampleText = defaultValue
            ? `Suggested default: ${defaultValue}\n\nOr enter your own path:`
            : `Examples:\n- Directory: /Users/username/Documents/MyFolder\n- File: /Users/username/Documents/file.txt`;

          alert(`Browser doesn't support file picking. Please enter the full path for ${displayName.toLowerCase()}.\n\n${exampleText}`);
          inputField.focus();
        }
      });
    });

    // Setup save button
    document.getElementById('saveFilepathSettingsBtn').addEventListener('click', () => {
      const form = document.getElementById('filepathSettingsForm');
      const formData = new FormData(form);
      const settings = {};

      for (const [key, value] of formData.entries()) {
        // If field is empty, use the placeholder value (which is the default from plugin)
        if (!value || value.trim() === '') {
          const inputField = document.getElementById(`filepath_${key}`);
          const placeholderValue = inputField.placeholder;

          // Only use placeholder if it's a real default value (not a generic example)
          if (defaultSettings[key]) {
            settings[key] = defaultSettings[key];
          } else {
            settings[key] = value; // Keep empty for validation below
          }
        } else {
          settings[key] = value;
        }
      }

      // Validate that all fields are filled (either by user input or default values)
      const emptyFields = Object.keys(filepathFields).filter(field => !settings[field] || settings[field].trim() === '');
      if (emptyFields.length > 0) {
        alert(`Please fill in all required fields: ${emptyFields.join(', ')}\n\nNote: You can leave fields empty to use the default values shown in the placeholders.`);
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

// Make functions globally available
window.showPluginConfigModal = showPluginConfigModal;
window.savePluginConfig = savePluginConfig;
window.checkPluginFilepathSettings = checkPluginFilepathSettings;
window.showFilepathSettingsModal = showFilepathSettingsModal;
window.enablePluginWithSettings = enablePluginWithSettings;

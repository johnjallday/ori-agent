// Plugin Configuration Module
// Handles all plugin configuration functionality including config modals

// Show plugin configuration modal
async function showPluginConfigModal(pluginName) {
  try {
    // Fetch plugin configuration info
    const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/config`);
    if (!response.ok) {
      throw new Error('Failed to fetch plugin configuration');
    }

    const configData = await response.json();

    // Handle modern plugins with required_config
    const configVars = configData.required_config || [];
    const currentValues = configData.current_values || {};

    // If no required_config but we have current_values, create form fields from existing values
    let finalConfigVars = configVars;
    if (configVars.length === 0 && Object.keys(currentValues).length > 0) {
      // Create config vars from existing values
      finalConfigVars = Object.keys(currentValues).map(key => ({
        name: key,
        type: 'text', // Default to text type
        description: `Configuration for ${key}`,
        required: false
      }));
    }

    if (finalConfigVars.length === 0) {
      // No configuration needed and no existing values
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
                ${configData.is_initialized ? 'Edit' : 'Configure'} ${pluginName}
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
            </div>
            <div class="modal-body">
              <form id="pluginConfigForm">
                <p style="color: var(--text-secondary); margin-bottom: 20px;">
                  ${configData.is_initialized ?
                    'Edit the configuration settings for this plugin:' :
                    'This plugin requires configuration before it can be used. Please provide the following information:'}
                </p>
                ${finalConfigVars.map(configVar => {
                  const currentValue = currentValues[configVar.name] || '';
                  return `
                  <div class="mb-3">
                    <label for="config_${configVar.name}" class="form-label" style="color: var(--text-primary);">
                      ${configVar.name}
                      ${configVar.required ? '<span style="color: var(--danger-color);">*</span>' : ''}
                    </label>
                    ${configVar.type === 'password' ? `
                      <input type="password" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.placeholder}"
                             value="${currentValue}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    ` : configVar.type === 'number' ? `
                      <input type="number" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.placeholder}"
                             value="${currentValue}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    ` : configVar.type === 'boolean' ? `
                      <div class="form-check">
                        <input type="checkbox" class="form-check-input" id="config_${configVar.name}" name="${configVar.name}"
                               ${currentValue ? 'checked' : ''}>
                        <label class="form-check-label" for="config_${configVar.name}" style="color: var(--text-secondary);">
                          ${configVar.placeholder}
                        </label>
                      </div>
                    ` : (configVar.name.includes('dir') || configVar.name.includes('path') || configVar.name.includes('template') || configVar.name.includes('file')) ? `
                      <div class="input-group">
                        <input type="text" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                               placeholder="${configVar.placeholder}"
                               value="${currentValue}"
                               ${configVar.required ? 'required' : ''}
                               style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                      </div>
                    ` : `
                      <input type="text" class="form-control" id="config_${configVar.name}" name="${configVar.name}"
                             placeholder="${configVar.placeholder}"
                             value="${currentValue}"
                             ${configVar.required ? 'required' : ''}
                             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
                    `}
                    <div class="form-text" style="color: var(--text-secondary);">
                      ${configVar.description}
                    </div>
                  </div>
                `;}).join('')}
              </form>
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"
                      style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">
                Cancel
              </button>
              <button type="button" class="btn btn-primary" id="savePluginConfigBtn"
                      style="background: var(--primary-color); border-color: var(--primary-color); color: white;">
                ${configData.is_initialized ? 'Update Configuration' : 'Save Configuration'}
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


    // Setup browse button event listeners
    document.querySelectorAll('.browse-btn').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        const fieldName = e.target.closest('button').dataset.field;
        const inputField = document.getElementById(`config_${fieldName}`);
        const isDirectory = fieldName.includes('dir');
        const isFile = fieldName.includes('template') || fieldName.includes('file');

        try {
          let selectedPath = '';

          if (isDirectory) {
            // Try visual directory picker first, fallback to manual entry
            try {
              if ('showDirectoryPicker' in window) {
                const dirHandle = await window.showDirectoryPicker();
                // Browser won't give us the full path, so ask user to provide it
                selectedPath = await promptWithVisualPicker(fieldName, true, dirHandle.name);
              } else {
                selectedPath = await promptForDirectoryPath(fieldName, true);
              }
            } catch (err) {
              if (err.name !== 'AbortError') {
                selectedPath = await promptForDirectoryPath(fieldName, true);
              }
              return;
            }
          } else if (isFile) {
            // Try visual file picker first, fallback to manual entry
            try {
              if ('showOpenFilePicker' in window) {
                const [fileHandle] = await window.showOpenFilePicker();
                // Browser won't give us the full path, so ask user to provide it
                selectedPath = await promptWithVisualPicker(fieldName, false, fileHandle.name);
              } else {
                selectedPath = await promptForDirectoryPath(fieldName, false);
              }
            } catch (err) {
              if (err.name !== 'AbortError') {
                selectedPath = await promptForDirectoryPath(fieldName, false);
              }
              return;
            }
          } else {
            // For generic path fields, ask what they want to select
            const choice = confirm(`What do you want to select?\n\nClick "OK" for directory/folder\nClick "Cancel" for file`);

            if (choice) {
              // Directory
              try {
                if ('showDirectoryPicker' in window) {
                  const dirHandle = await window.showDirectoryPicker();
                  selectedPath = await promptWithVisualPicker(fieldName, true, dirHandle.name);
                } else {
                  selectedPath = await promptForDirectoryPath(fieldName, true);
                }
              } catch (err) {
                if (err.name !== 'AbortError') {
                  selectedPath = await promptForDirectoryPath(fieldName, true);
                }
                return;
              }
            } else {
              // File
              try {
                if ('showOpenFilePicker' in window) {
                  const [fileHandle] = await window.showOpenFilePicker();
                  selectedPath = await promptWithVisualPicker(fieldName, false, fileHandle.name);
                } else {
                  selectedPath = await promptForDirectoryPath(fieldName, false);
                }
              } catch (err) {
                if (err.name !== 'AbortError') {
                  selectedPath = await promptForDirectoryPath(fieldName, false);
                }
                return;
              }
            }
          }

          if (selectedPath && selectedPath.trim() !== '') {
            inputField.value = selectedPath.trim();
          }

        } catch (error) {
          console.error('Error browsing for path:', error);

          // Fallback to path suggestions
          const displayName = fieldName.split('_').map(word =>
            word.charAt(0).toUpperCase() + word.slice(1)
          ).join(' ');

          let suggestedPath = '';
          if (fieldName.toLowerCase().includes('project')) {
            suggestedPath = navigator.platform.includes('Mac') ? '/Users/username/Music/Projects' : 'C:\\Users\\user\\Music\\Projects';
          } else if (fieldName.toLowerCase().includes('template')) {
            if (isFile) {
              suggestedPath = navigator.platform.includes('Mac') ? '/Users/username/Library/Application Support/REAPER/ProjectTemplates/Default.RPP' : 'C:\\Users\\user\\AppData\\Roaming\\REAPER\\ProjectTemplates\\Default.RPP';
            } else {
              suggestedPath = navigator.platform.includes('Mac') ? '/Users/username/Library/Application Support/REAPER/ProjectTemplates' : 'C:\\Users\\user\\AppData\\Roaming\\REAPER\\ProjectTemplates';
            }
          } else {
            suggestedPath = navigator.platform.includes('Mac') ?
              (isFile ? '/Users/username/Documents/file.txt' : '/Users/username/Documents') :
              (isFile ? 'C:\\Users\\user\\Documents\\file.txt' : 'C:\\Users\\user\\Documents');
          }

          const errorMsg = error.message.includes('not supported') ?
            `Your browser doesn't support the file/directory picker.` :
            `Error accessing file system: ${error.message}`;

          alert(`${errorMsg}\n\nPlease enter the path for ${displayName.toLowerCase()} manually.\n\nSuggested path: ${suggestedPath}`);

          inputField.value = suggestedPath;
          inputField.focus();
          inputField.select();
        }
      });
    });

    // Setup save button event listener
    document.getElementById('savePluginConfigBtn').addEventListener('click', async () => {
      await savePluginConfig(pluginName, finalConfigVars);
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

    // Send configuration to server using the save-settings endpoint
    const response = await fetch('/api/plugins/save-settings', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        plugin_name: pluginName,
        settings: configData
      })
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

// Cross-browser helper functions
async function constructPathFromDirHandle(dirHandle) {
  try {
    // Try to get the full path if available (newer browsers)
    if (dirHandle.getDirectoryHandle) {
      // Try to resolve the full path by traversing up to get context
      let pathParts = [dirHandle.name];
      let current = dirHandle;

      // This is a simplified approach - in practice, getting full paths
      // from directory handles is limited for security reasons
      // We'll use the directory name as the final component
      return `/Users/jj/${dirHandle.name}`;
    }

    // Fallback: just use the directory name
    return `/Users/jj/${dirHandle.name}`;

  } catch (error) {
    console.error('Error constructing path from directory handle:', error);
    // Ultimate fallback
    return `/Users/jj/${dirHandle.name}`;
  }
}

function constructPathFromFileHandle(fileHandle, fieldName) {
  const fileName = fileHandle.name;
  const username = 'jj';

  if (fieldName.toLowerCase().includes('template')) {
    return navigator.platform.includes('Mac') ?
      `/Users/${username}/Library/Application Support/REAPER/ProjectTemplates/${fileName}` :
      `C:\\\\Users\\\\${username}\\\\AppData\\\\Roaming\\\\REAPER\\\\ProjectTemplates\\\\${fileName}`;
  } else {
    return navigator.platform.includes('Mac') ?
      `/Users/${username}/Documents/${fileName}` :
      `C:\\\\Users\\\\${username}\\\\Documents\\\\${fileName}`;
  }
}

// Fallback for Firefox, Safari, and other browsers
async function selectDirectoryFallback(fieldName) {
  return new Promise((resolve) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.webkitdirectory = true;
    input.multiple = false;
    input.style.display = 'none';

    input.onchange = (event) => {
      const files = event.target.files;
      if (files.length > 0) {
        const firstFile = files[0];
        const relativePath = firstFile.webkitRelativePath;
        const dirName = relativePath.split('/')[0];

        const username = 'jj';
        let constructedPath;

        // Construct path based on detected directory name
        if (navigator.platform.includes('Mac')) {
          if (dirName.toLowerCase().includes('document')) {
            constructedPath = `/Users/${username}/Documents`;
          } else if (dirName.toLowerCase().includes('music')) {
            constructedPath = `/Users/${username}/Music`;
          } else if (dirName.toLowerCase().includes('desktop')) {
            constructedPath = `/Users/${username}/Desktop`;
          } else if (dirName.toLowerCase().includes('picture')) {
            constructedPath = `/Users/${username}/Pictures`;
          } else if (dirName.toLowerCase() === 'projects') {
            constructedPath = `/Users/${username}/Music/Projects`;
          } else if (dirName.toLowerCase().includes('template')) {
            constructedPath = `/Users/${username}/Library/Application Support/REAPER/ProjectTemplates`;
          } else {
            constructedPath = `/Users/${username}/${dirName}`;
          }
        } else {
          if (dirName.toLowerCase().includes('document')) {
            constructedPath = `C:\\\\Users\\\\${username}\\\\Documents`;
          } else if (dirName.toLowerCase().includes('music')) {
            constructedPath = `C:\\\\Users\\\\${username}\\\\Music`;
          } else if (dirName.toLowerCase().includes('desktop')) {
            constructedPath = `C:\\\\Users\\\\${username}\\\\Desktop`;
          } else if (dirName.toLowerCase().includes('picture')) {
            constructedPath = `C:\\\\Users\\\\${username}\\\\Pictures`;
          } else if (dirName.toLowerCase() === 'projects') {
            constructedPath = `C:\\\\Users\\\\${username}\\\\Music\\\\Projects`;
          } else if (dirName.toLowerCase().includes('template')) {
            constructedPath = `C:\\\\Users\\\\${username}\\\\AppData\\\\Roaming\\\\REAPER\\\\ProjectTemplates`;
          } else {
            constructedPath = `C:\\\\Users\\\\${username}\\\\${dirName}`;
          }
        }

        console.log(`Fallback directory selection: ${dirName} → ${constructedPath}`);
        resolve(constructedPath);
      } else {
        resolve(null); // No files selected
      }

      // Clean up
      document.body.removeChild(input);
    };

    input.oncancel = () => {
      resolve(null);
      document.body.removeChild(input);
    };

    // Add to DOM temporarily and trigger click
    document.body.appendChild(input);
    input.click();
  });
}

async function selectFileFallback(fieldName) {
  return new Promise((resolve) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.multiple = false;
    input.style.display = 'none';

    // Set accept filter based on field type
    if (fieldName.toLowerCase().includes('template')) {
      input.accept = '.rpp,.RPP';
    }

    input.onchange = (event) => {
      const file = event.target.files[0];
      if (file) {
        const fileName = file.name;
        const username = 'jj';
        let constructedPath;

        if (fieldName.toLowerCase().includes('template')) {
          constructedPath = navigator.platform.includes('Mac') ?
            `/Users/${username}/Library/Application Support/REAPER/ProjectTemplates/${fileName}` :
            `C:\\\\Users\\\\${username}\\\\AppData\\\\Roaming\\\\REAPER\\\\ProjectTemplates\\\\${fileName}`;
        } else {
          constructedPath = navigator.platform.includes('Mac') ?
            `/Users/${username}/Documents/${fileName}` :
            `C:\\\\Users\\\\${username}\\\\Documents\\\\${fileName}`;
        }

        console.log(`Fallback file selection: ${fileName} → ${constructedPath}`);
        resolve(constructedPath);
      } else {
        resolve(null);
      }

      // Clean up
      document.body.removeChild(input);
    };

    input.oncancel = () => {
      resolve(null);
      document.body.removeChild(input);
    };

    // Add to DOM temporarily and trigger click
    document.body.appendChild(input);
    input.click();
  });
}

// Prompt user for directory/file path
async function promptWithVisualPicker(fieldName, isDirectory, selectedName) {
  const displayName = fieldName.split('_').map(word =>
    word.charAt(0).toUpperCase() + word.slice(1)
  ).join(' ');

  const pathType = isDirectory ? 'directory' : 'file';
  let suggestedPath = '';

  // Provide better defaults based on field name
  if (fieldName.toLowerCase().includes('project')) {
    suggestedPath = `/Users/jj/Music/Projects${isDirectory ? `/${selectedName}` : `/${selectedName}`}`;
  } else if (fieldName.toLowerCase().includes('template')) {
    if (isDirectory) {
      suggestedPath = `/Users/jj/Library/Application Support/REAPER/ProjectTemplates/${selectedName}`;
    } else {
      suggestedPath = `/Users/jj/Library/Application Support/REAPER/ProjectTemplates/${selectedName}`;
    }
  } else {
    suggestedPath = isDirectory ? `/Users/jj/Documents/${selectedName}` : `/Users/jj/Documents/${selectedName}`;
  }

  const message = `You selected "${selectedName}" from the ${pathType} picker.\n\n` +
    `Please enter the full path to this ${pathType}:\n\n` +
    `Note: Browsers don't reveal actual paths for security, so you need to provide the full path.`;

  const userPath = prompt(message, suggestedPath);

  if (userPath && userPath.trim() !== '') {
    return userPath.trim();
  }

  return null;
}

async function promptForDirectoryPath(fieldName, isDirectory) {
  const displayName = fieldName.split('_').map(word =>
    word.charAt(0).toUpperCase() + word.slice(1)
  ).join(' ');

  const pathType = isDirectory ? 'directory' : 'file';
  let suggestedPath = '';

  // Provide better defaults based on field name
  if (fieldName.toLowerCase().includes('project')) {
    suggestedPath = '/Users/jj/Music/Projects';
  } else if (fieldName.toLowerCase().includes('template')) {
    if (isDirectory) {
      suggestedPath = '/Users/jj/Library/Application Support/REAPER/ProjectTemplates';
    } else {
      suggestedPath = '/Users/jj/Library/Application Support/REAPER/ProjectTemplates/Default.RPP';
    }
  } else {
    suggestedPath = isDirectory ? '/Users/jj/Documents' : '/Users/jj/Documents/file.txt';
  }

  const message = `Please enter the full path to the ${pathType} for "${displayName}":\n\n` +
    `Example: ${suggestedPath}\n\n` +
    `Note: Due to browser security restrictions, you need to type the full path manually.`;

  const userPath = prompt(message, suggestedPath);

  if (userPath && userPath.trim() !== '') {
    return userPath.trim();
  }

  return null; // User cancelled or entered empty path
}

// Handle file upload for config files
async function handleFileUpload(fieldName, inputField) {
  try {
    // Create file input element
    const fileInput = document.createElement('input');
    fileInput.type = 'file';
    fileInput.accept = '.json,.txt,.config,.yaml,.yml';
    fileInput.style.display = 'none';

    // Handle file selection
    fileInput.addEventListener('change', async (event) => {
      const file = event.target.files[0];
      if (!file) return;

      try {
        // Read file content
        const content = await file.text();

        // Try to parse as JSON to validate
        let configData;
        try {
          configData = JSON.parse(content);
        } catch (parseError) {
          // If not JSON, treat as plain text
          configData = { [fieldName]: content };
        }

        // Extract plugin name from modal
        const modal = document.getElementById('pluginConfigModal');
        const modalTitle = modal.querySelector('.modal-title').textContent;
        const pluginName = modalTitle.replace('Configure ', '').replace(' Plugin', '').trim();

        // Upload config file
        const response = await fetch('/api/plugins/upload-config', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            plugin_name: pluginName,
            config: configData,
            filename: file.name,
            field_name: fieldName
          })
        });

        if (response.ok) {
          const result = await response.json();

          // Update input field with the saved path
          inputField.value = result.saved_path || file.name;

          // Show success message
          alert(`✅ Successfully uploaded ${file.name}!\n\nFile saved to: ${result.saved_path}`);

          console.log('File uploaded successfully:', result);
        } else {
          const error = await response.text();
          throw new Error(`Upload failed: ${error}`);
        }

      } catch (error) {
        console.error('File upload error:', error);
        alert(`❌ Upload failed: ${error.message}`);
      }
    });

    // Trigger file picker
    document.body.appendChild(fileInput);
    fileInput.click();

    // Clean up
    setTimeout(() => {
      document.body.removeChild(fileInput);
    }, 1000);

  } catch (error) {
    console.error('File upload setup error:', error);
    alert(`❌ Error setting up file upload: ${error.message}`);
  }
}

// Browser detection helper
function getBrowserName() {
  const isChrome = /Chrome/.test(navigator.userAgent) && /Google Inc/.test(navigator.vendor);
  const isEdge = /Edg/.test(navigator.userAgent);
  const isFirefox = /Firefox/.test(navigator.userAgent);
  const isSafari = /Safari/.test(navigator.userAgent) && !/Chrome/.test(navigator.userAgent);

  if (isChrome) return 'Chrome';
  if (isEdge) return 'Edge';
  if (isFirefox) return 'Firefox';
  if (isSafari) return 'Safari';
  return 'Unknown';
}

// Make functions globally available
window.showPluginConfigModal = showPluginConfigModal;
window.savePluginConfig = savePluginConfig;
window.constructPathFromDirHandle = constructPathFromDirHandle;
window.constructPathFromFileHandle = constructPathFromFileHandle;
window.selectDirectoryFallback = selectDirectoryFallback;
window.selectFileFallback = selectFileFallback;
window.getBrowserName = getBrowserName;

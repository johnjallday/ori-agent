// Settings Management Module
// Handles all settings-related functionality including loading, saving, and UI management

// Settings Management Functions

// Show notification function
function showNotification(message, type = 'info') {
  // Create notification element
  const notification = document.createElement('div');
  notification.className = `alert alert-${type === 'success' ? 'success' : type === 'error' ? 'danger' : 'info'} position-fixed`;
  notification.style.cssText = `
    top: 20px;
    right: 20px;
    z-index: 1050;
    max-width: 300px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.15);
    border: none;
  `;
  notification.innerHTML = `
    <div class="d-flex align-items-center">
      <span>${message}</span>
      <button type="button" class="btn-close ms-2" aria-label="Close"></button>
    </div>
  `;
  
  // Add to body
  document.body.appendChild(notification);
  
  // Add close functionality
  const closeBtn = notification.querySelector('.btn-close');
  closeBtn.addEventListener('click', () => {
    notification.remove();
  });
  
  // Auto-remove after 3 seconds
  setTimeout(() => {
    if (document.body.contains(notification)) {
      notification.remove();
    }
  }, 3000);
}

// Load current settings
async function loadSettings() {
  try {
    const response = await fetch('/api/settings');
    if (response.ok) {
      const settings = await response.json();
      
      // Update model dropdown
      const modelSelect = document.getElementById('gptModelSelect');
      // Handle both nested (settings.Settings.model) and flat (settings.model) formats
      const modelValue = (settings.Settings && settings.Settings.model) || settings.model;
      if (modelSelect && modelValue) {
        modelSelect.value = modelValue;
      }
      
      // Update temperature slider
      const temperatureSlider = document.getElementById('temperatureSlider');
      const temperatureValue = document.getElementById('temperatureValue');
      // Handle both nested (settings.Settings.temperature) and flat (settings.temperature) formats
      const temperatureValueData = (settings.Settings && typeof settings.Settings.temperature !== 'undefined') 
        ? settings.Settings.temperature 
        : settings.temperature;
      if (temperatureSlider && typeof temperatureValueData !== 'undefined') {
        temperatureSlider.value = temperatureValueData;
        if (temperatureValue) {
          temperatureValue.textContent = temperatureValueData.toFixed(1);
        }
      }
    } else {
      console.error('Failed to load settings:', response.status);
    }
  } catch (error) {
    console.error('Error loading settings:', error);
    // Fallback to defaults
    const modelSelect = document.getElementById('gptModelSelect');
    const temperatureSlider = document.getElementById('temperatureSlider');
    const temperatureValue = document.getElementById('temperatureValue');
    
    if (modelSelect) modelSelect.value = 'gpt-4o';
    if (temperatureSlider) temperatureSlider.value = 0;
    if (temperatureValue) temperatureValue.textContent = '0.0';
  }
}

// Save settings
async function saveSettings() {
  try {
    const modelSelect = document.getElementById('gptModelSelect');
    const temperatureSlider = document.getElementById('temperatureSlider');
    
    if (!modelSelect || !temperatureSlider) {
      console.error('Settings elements not found');
      return;
    }
    
    const response = await fetch('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ 
        model: modelSelect.value,
        temperature: parseFloat(temperatureSlider.value)
      })
    });
    
    if (response.ok) {
      console.log('Settings updated:', {
        model: modelSelect.value,
        temperature: parseFloat(temperatureSlider.value)
      });
      
      // Show success notification
      showNotification('Settings updated successfully!', 'success');
    } else {
      console.error('Failed to save settings:', response.status);
      showNotification('Failed to save settings', 'error');
    }
  } catch (error) {
    console.error('Error saving settings:', error);
    showNotification('Error saving settings', 'error');
  }
}

// Settings Management
function toggleSetting(settingName, enabled) {
  console.log('Toggling setting:', settingName, 'enabled:', enabled);
  // Save setting to localStorage or send to server
  localStorage.setItem(settingName, String(enabled));
}

// Setup settings event listeners
function setupSettings() {
  const modelSelect = document.getElementById('gptModelSelect');
  const temperatureSlider = document.getElementById('temperatureSlider');
  const temperatureValue = document.getElementById('temperatureValue');
  const temperatureInput = document.getElementById('temperatureInput');
  const updateBtn = document.getElementById('updateSettingsBtn');
  
  if (temperatureSlider && temperatureValue) {
    temperatureSlider.addEventListener('input', function(e) {
      temperatureValue.textContent = parseFloat(e.target.value).toFixed(1);
    });
  }
  
  // Temperature click-to-edit functionality
  if (temperatureValue && temperatureInput && temperatureSlider) {
    // Click on value to edit
    temperatureValue.addEventListener('click', function() {
      temperatureInput.value = parseFloat(temperatureValue.textContent);
      temperatureValue.style.display = 'none';
      temperatureInput.style.display = 'inline-block';
      temperatureInput.focus();
      temperatureInput.select();
    });
    
    // Handle input changes
    function updateTemperatureFromInput() {
      const value = parseFloat(temperatureInput.value);
      if (!isNaN(value) && value >= 0 && value <= 2) {
        temperatureSlider.value = value;
        temperatureValue.textContent = value.toFixed(1);
      }
      temperatureInput.style.display = 'none';
      temperatureValue.style.display = 'inline-block';
    }
    
    // Save on Enter or blur
    temperatureInput.addEventListener('blur', updateTemperatureFromInput);
    temperatureInput.addEventListener('keydown', function(e) {
      if (e.key === 'Enter') {
        updateTemperatureFromInput();
      } else if (e.key === 'Escape') {
        temperatureInput.style.display = 'none';
        temperatureValue.style.display = 'inline-block';
      }
    });
  }
  
  if (updateBtn) {
    updateBtn.addEventListener('click', function() {
      saveSettings();
      console.log('Settings saved to config.json');
    });
  }
  
  // Settings buttons
  const advancedSettingsBtn = document.getElementById('advancedSettingsBtn');
  if (advancedSettingsBtn) {
    advancedSettingsBtn.addEventListener('click', () => {
      console.log('Advanced settings clicked');
      // Show advanced settings modal
    });
  }

  // System buttons
  const systemDiagnosticsBtn = document.getElementById('systemDiagnosticsBtn');
  if (systemDiagnosticsBtn) {
    systemDiagnosticsBtn.addEventListener('click', () => {
      console.log('System diagnostics clicked');
      // Show system diagnostics panel
    });
  }

  // Settings toggle switches
  document.querySelectorAll('.setting-item .form-check-input').forEach(toggle => {
    toggle.addEventListener('change', (e) => {
      const settingName = e.target.closest('.setting-item').querySelector('span').textContent;
      toggleSetting(settingName, e.target.checked);
    });
  });
  
  // Load current settings
  loadSettings();
  
  console.log('Settings management setup complete');
}

// Initialize settings management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupSettings);
} else {
  setupSettings();
}
// Settings Page JavaScript Module
// Uses SettingsController for notifications and state management

// Helper function to get notification handler
function notify(message, type = 'info') {
  if (typeof SettingsController !== 'undefined') {
    SettingsController.notify(message, type);
  } else if (typeof Toast !== 'undefined') {
    Toast[type] ? Toast[type](message) : Toast.info(message);
  } else {
    alert(type === 'error' ? 'Error: ' + message : message);
  }
}

// Toggle password visibility for API keys
document.getElementById('toggleOpenaiKey')?.addEventListener('click', function() {
  const input = document.getElementById('openaiApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

document.getElementById('toggleAnthropicKey')?.addEventListener('click', function() {
  const input = document.getElementById('anthropicApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

document.getElementById('toggleGeminiKey')?.addEventListener('click', function() {
  const input = document.getElementById('geminiApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

// Save OpenAI API Key
document.getElementById('saveOpenaiKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('openaiApiKeyInput').value.trim();

  if (!apiKey) {
    notify('Please enter an API key', 'warning');
    return;
  }

  if (!apiKey.startsWith('sk-')) {
    notify('Invalid API key format. OpenAI keys start with "sk-"', 'warning');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ openai_api_key: apiKey })
    });

    if (response.ok) {
      console.log('[settings] OpenAI API key updated');
      notify('OpenAI API key saved successfully!', 'success');
      document.getElementById('openaiApiKeyInput').value = '';
    } else {
      const error = await response.text();
      notify('Failed to save API key: ' + error, 'error');
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    notify('Error saving API key: ' + error.message, 'error');
  }
});

// Save Anthropic API Key
document.getElementById('saveAnthropicKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('anthropicApiKeyInput').value.trim();

  if (!apiKey) {
    notify('Please enter an API key', 'warning');
    return;
  }

  if (!apiKey.startsWith('sk-ant-')) {
    notify('Invalid API key format. Anthropic keys start with "sk-ant-"', 'warning');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        anthropic_api_key: apiKey
      })
    });

    if (response.ok) {
      console.log('[settings] Anthropic API key updated');
      notify('Anthropic API key saved successfully!', 'success');
      document.getElementById('anthropicApiKeyInput').value = '';
    } else {
      const error = await response.text();
      notify('Failed to save API key: ' + error, 'error');
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    notify('Error saving API key: ' + error.message, 'error');
  }
});

// Toggle Claude setup token visibility
document.getElementById('toggleClaudeSetupToken')?.addEventListener('click', function() {
  const input = document.getElementById('claudeSetupTokenInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

// Save Claude Setup Token
document.getElementById('saveClaudeSetupToken')?.addEventListener('click', async function() {
  const token = document.getElementById('claudeSetupTokenInput').value.trim();

  if (!token) {
    notify('Please enter a setup token', 'warning');
    return;
  }

  if (!token.startsWith('sk-ant-oat01-') || token.length < 80) {
    notify('Invalid setup token. Must start with "sk-ant-oat01-" and be at least 80 characters.', 'warning');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ anthropic_api_key: token })
    });

    if (response.ok) {
      const data = await response.json();
      console.log('[settings] Claude setup token exchanged for API key', data);
      notify('Setup token exchanged — Claude API key saved successfully!', 'success');
      document.getElementById('claudeSetupTokenInput').value = '';
    } else {
      const error = await response.text();
      notify('Failed to exchange setup token: ' + error, 'error');
    }
  } catch (error) {
    console.error('Error saving setup token:', error);
    notify('Error saving setup token: ' + error.message, 'error');
  }
});

// Save Gemini API Key
document.getElementById('saveGeminiKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('geminiApiKeyInput').value.trim();

  if (!apiKey) {
    notify('Please enter an API key', 'warning');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ gemini_api_key: apiKey })
    });

    if (response.ok) {
      console.log('[settings] Gemini API key updated');
      notify('Gemini API key saved successfully!', 'success');
      document.getElementById('geminiApiKeyInput').value = '';
    } else {
      const error = await response.text();
      notify('Failed to save API key: ' + error, 'error');
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    notify('Error saving API key: ' + error.message, 'error');
  }
});

// System Diagnostics Button
document.getElementById('systemDiagnosticsBtn')?.addEventListener('click', async function() {
  try {
    const response = await fetch('/api/updates/version');
    const data = await response.json();

    notify(`System Diagnostics - Version: ${data.version || 'Unknown'}, Status: Online`, 'info');
  } catch (error) {
    console.error('Error fetching diagnostics:', error);
    notify('Error fetching system diagnostics', 'error');
  }
});

// Utility Web Search Settings
(function() {
  const providerSelect = document.getElementById('utilitySearchProvider');
  const saveProviderBtn = document.getElementById('saveUtilitySearchSettingsBtn');
  const openApiModalBtn = document.getElementById('openSearchApiModalBtn');
  const keyStatusEl = document.getElementById('utilityBraveApiKeyStatus');
  const keyMaskedEl = document.getElementById('utilityBraveApiKeyMasked');
  const statusIndicator = document.getElementById('utilitySearchStatusIndicator');
  const statusText = document.getElementById('utilitySearchStatusText');
  const statusDetails = document.getElementById('utilitySearchStatusDetails');

  const searchApiModalEl = document.getElementById('searchApiModal');
  const searchApiInput = document.getElementById('searchApiBraveKeyInput');
  const currentKeyText = document.getElementById('searchApiCurrentKeyText');
  const saveApiKeyBtn = document.getElementById('saveSearchApiKeyBtn');
  const removeApiKeyBtn = document.getElementById('removeSearchApiKeyBtn');
  const toggleApiKeyBtn = document.getElementById('toggleSearchApiKey');

  if (!providerSelect) {
    return;
  }

  let utility = null;
  let searchApiModal = null;

  function normalizeProvider(provider) {
    const normalized = String(provider || '').toLowerCase();
    switch (normalized) {
      case 'brave':
      case 'duckduckgo':
      case 'auto':
        return normalized;
      default:
        return 'auto';
    }
  }

  function setButtonLoading(btn, loading, loadingLabel) {
    if (!btn) return;
    if (loading) {
      btn.dataset.originalLabel = btn.innerHTML;
      btn.disabled = true;
      btn.innerHTML = `<span class="spinner-border spinner-border-sm me-2"></span>${loadingLabel}`;
      return;
    }

    btn.disabled = false;
    if (btn.dataset.originalLabel) {
      btn.innerHTML = btn.dataset.originalLabel;
    }
  }

  function updateStatus(provider, hasBraveKey) {
    if (!statusIndicator || !statusText || !statusDetails) return;

    const icon = {
      success: `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
        </svg>
      `,
      warn: `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#f59e0b">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
      `,
      info: `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#3b82f6">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M13,17H11V11H13V17M13,9H11V7H13V9Z"/>
        </svg>
      `
    };

    if (provider === 'brave' && !hasBraveKey) {
      statusIndicator.innerHTML = icon.warn;
      statusText.textContent = 'Brave Search selected, API key missing';
      statusDetails.textContent = 'Web search will fall back to DuckDuckGo until a Brave key is added.';
      return;
    }

    if (provider === 'duckduckgo') {
      statusIndicator.innerHTML = icon.info;
      statusText.textContent = 'DuckDuckGo search active';
      statusDetails.textContent = 'No API key required.';
      return;
    }

    if (provider === 'auto' && hasBraveKey) {
      statusIndicator.innerHTML = icon.success;
      statusText.textContent = 'Auto mode with Brave Search available';
      statusDetails.textContent = 'Brave is used by default, with safe fallback behavior.';
      return;
    }

    statusIndicator.innerHTML = icon.info;
    statusText.textContent = 'Auto mode using DuckDuckGo';
    statusDetails.textContent = 'Add a Brave key to upgrade search quality in auto mode.';
  }

  function updateKeySummary(hasBraveKey, maskedKey) {
    if (keyStatusEl) {
      keyStatusEl.textContent = hasBraveKey ? 'Configured' : 'Not configured';
    }
    if (keyMaskedEl) {
      keyMaskedEl.textContent = hasBraveKey ? (maskedKey || '***') : 'Not configured';
    }
    if (currentKeyText) {
      currentKeyText.textContent = hasBraveKey
        ? `Current key: ${maskedKey || '***'}`
        : 'No Brave API key is currently stored.';
    }
  }

  function applyUtilitySettings(utilitySettings) {
    utility = utilitySettings || {};
    const provider = normalizeProvider(utility.search_provider);
    const hasBraveKey = Boolean(utility.has_brave_api_key);
    const masked = utility.brave_api_key_masked || '';

    providerSelect.value = provider;
    updateKeySummary(hasBraveKey, masked);
    updateStatus(provider, hasBraveKey);
  }

  async function loadUtilitySettings() {
    try {
      const response = await fetch('/api/settings/utility');
      if (!response.ok) {
        throw new Error(await response.text() || 'Failed to load utility settings');
      }
      const data = await response.json();
      applyUtilitySettings(data.utility || {});
    } catch (error) {
      console.error('Failed to load utility settings:', error);
      if (statusIndicator) {
        statusIndicator.innerHTML = `
          <svg width="24" height="24" viewBox="0 0 24 24" fill="#ef4444">
            <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
          </svg>
        `;
      }
      if (statusText) {
        statusText.textContent = 'Failed to load web search settings';
      }
      if (statusDetails) {
        statusDetails.textContent = error.message || '';
      }
    }
  }

  async function saveUtilitySettings(payload) {
    const response = await fetch('/api/settings/utility', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      let message = await response.text();
      if (!message) {
        message = 'Failed to save utility settings';
      }
      throw new Error(message);
    }

    const data = await response.json();
    applyUtilitySettings(data.utility || {});
    return data.utility || {};
  }

  saveProviderBtn?.addEventListener('click', async function() {
    const provider = normalizeProvider(providerSelect.value);
    setButtonLoading(saveProviderBtn, true, 'Saving...');
    try {
      await saveUtilitySettings({ search_provider: provider });
      notify('Web search settings saved.', 'success');
      if (provider === 'brave' && !utility?.has_brave_api_key) {
        notify('Brave Search requires an API key. Add one in "Manage API Key".', 'warning');
      }
    } catch (error) {
      console.error('Failed to save web search settings:', error);
      notify('Failed to save web search settings: ' + error.message, 'error');
    } finally {
      setButtonLoading(saveProviderBtn, false);
    }
  });

  openApiModalBtn?.addEventListener('click', function() {
    if (!searchApiModalEl) return;
    if (!searchApiModal) {
      searchApiModal = new bootstrap.Modal(searchApiModalEl);
    }
    if (searchApiInput) {
      searchApiInput.value = '';
      searchApiInput.type = 'password';
    }
    updateKeySummary(Boolean(utility?.has_brave_api_key), utility?.brave_api_key_masked || '');
    searchApiModal.show();
  });

  toggleApiKeyBtn?.addEventListener('click', function() {
    if (!searchApiInput) return;
    searchApiInput.type = searchApiInput.type === 'password' ? 'text' : 'password';
  });

  saveApiKeyBtn?.addEventListener('click', async function() {
    const braveKey = searchApiInput?.value.trim() || '';
    if (!braveKey) {
      notify('Enter a Brave API key, or use "Remove Key".', 'warning');
      return;
    }

    setButtonLoading(saveApiKeyBtn, true, 'Saving...');
    try {
      await saveUtilitySettings({ brave_api_key: braveKey });
      notify('Brave API key saved.', 'success');
      if (searchApiInput) {
        searchApiInput.value = '';
        searchApiInput.type = 'password';
      }
      if (searchApiModal) {
        searchApiModal.hide();
      }
    } catch (error) {
      console.error('Failed to save Brave API key:', error);
      notify('Failed to save Brave API key: ' + error.message, 'error');
    } finally {
      setButtonLoading(saveApiKeyBtn, false);
    }
  });

  removeApiKeyBtn?.addEventListener('click', async function() {
    if (!utility?.has_brave_api_key) {
      notify('No Brave API key is stored.', 'info');
      return;
    }

    if (!confirm('Remove the stored Brave API key?')) {
      return;
    }

    setButtonLoading(removeApiKeyBtn, true, 'Removing...');
    try {
      await saveUtilitySettings({ brave_api_key: '' });
      notify('Brave API key removed.', 'success');
      if (searchApiInput) {
        searchApiInput.value = '';
        searchApiInput.type = 'password';
      }
    } catch (error) {
      console.error('Failed to remove Brave API key:', error);
      notify('Failed to remove Brave API key: ' + error.message, 'error');
    } finally {
      setButtonLoading(removeApiKeyBtn, false);
    }
  });

  searchApiModalEl?.addEventListener('hidden.bs.modal', function() {
    if (!searchApiInput) return;
    searchApiInput.value = '';
    searchApiInput.type = 'password';
  });

  loadUtilitySettings();
})();

// Session Management Settings
(function() {
  const sessionCleanupEnabled = document.getElementById('sessionCleanupEnabled');
  const sessionCleanupDays = document.getElementById('sessionCleanupDays');
  const sessionMaxCount = document.getElementById('sessionMaxCount');
  const saveSessionSettingsBtn = document.getElementById('saveSessionSettingsBtn');
  const runSessionCleanupBtn = document.getElementById('runSessionCleanupBtn');
  const sessionTotalCount = document.getElementById('sessionTotalCount');
  const sessionInactiveCount = document.getElementById('sessionInactiveCount');
  const sessionDbSize = document.getElementById('sessionDbSize');

  // Load session settings and stats on page load
  async function loadSessionSettings() {
    try {
      // Load settings
      const settingsResp = await fetch('/api/settings');
      if (settingsResp.ok) {
        const settings = await settingsResp.json();
        if (sessionCleanupEnabled) {
          sessionCleanupEnabled.checked = settings.session_cleanup_enabled !== false;
        }
        if (sessionCleanupDays && settings.session_cleanup_days > 0) {
          sessionCleanupDays.value = settings.session_cleanup_days;
        }
        if (sessionMaxCount && settings.session_max_count > 0) {
          sessionMaxCount.value = settings.session_max_count;
        }
      }

      // Load storage stats
      await loadSessionStats();
    } catch (error) {
      console.error('Error loading session settings:', error);
    }
  }

  async function loadSessionStats() {
    try {
      const statsResp = await fetch('/api/sessions/storage/stats');
      if (statsResp.ok) {
        const stats = await statsResp.json();
        if (sessionTotalCount) {
          sessionTotalCount.textContent = stats.total_sessions || 0;
        }
        if (sessionInactiveCount) {
          sessionInactiveCount.textContent = stats.inactive_sessions_30_days || 0;
        }
        if (sessionDbSize) {
          sessionDbSize.textContent = formatBytes(stats.database_size_bytes || 0);
        }
      }
    } catch (error) {
      console.error('Error loading session stats:', error);
    }
  }

  function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  // Save session settings
  saveSessionSettingsBtn?.addEventListener('click', async function() {
    try {
      const response = await fetch('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          session_cleanup_enabled: sessionCleanupEnabled?.checked ?? true,
          session_cleanup_days: parseInt(sessionCleanupDays?.value || '30', 10),
          session_max_count: parseInt(sessionMaxCount?.value || '1000', 10)
        })
      });

      if (response.ok) {
        showToast('Session settings saved successfully!', 'success');
      } else {
        const error = await response.text();
        showToast('Failed to save settings: ' + error, 'error');
      }
    } catch (error) {
      console.error('Error saving session settings:', error);
      showToast('Error saving settings: ' + error.message, 'error');
    }
  });

  // Run cleanup now
  runSessionCleanupBtn?.addEventListener('click', async function() {
    const days = parseInt(sessionCleanupDays?.value || '30', 10);

    if (!confirm(`This will permanently delete all sessions inactive for ${days}+ days. Continue?`)) {
      return;
    }

    runSessionCleanupBtn.disabled = true;
    runSessionCleanupBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Cleaning...';

    try {
      const response = await fetch('/api/sessions/cleanup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ days: days })
      });

      if (response.ok) {
        const result = await response.json();
        showToast(`Cleanup complete! Deleted ${result.deleted || 0} sessions.`, 'success');
        await loadSessionStats(); // Refresh stats
      } else {
        const error = await response.text();
        showToast('Cleanup failed: ' + error, 'error');
      }
    } catch (error) {
      console.error('Error running cleanup:', error);
      showToast('Error running cleanup: ' + error.message, 'error');
    } finally {
      runSessionCleanupBtn.disabled = false;
      runSessionCleanupBtn.innerHTML = `
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
        </svg>
        Run Cleanup Now
      `;
    }
  });

  function showToast(message, type = 'info') {
    // Use the module-level notify function
    notify(message, type);
  }

  // Load settings on page load
  if (document.getElementById('sessionCleanupEnabled')) {
    loadSessionSettings();
  }
})();

// Reset Application functionality
(function() {
  const resetCheckboxes = {
    settings: document.getElementById('resetSettings'),
    agents: document.getElementById('resetAgents'),
    sessions: document.getElementById('resetSessions'),
    plugins: document.getElementById('resetPlugins'),
    onboarding: document.getElementById('resetOnboarding')
  };

  const resetAppBtn = document.getElementById('resetAppBtn');
  const selectAllBtn = document.getElementById('selectAllResetBtn');
  const clearAllBtn = document.getElementById('clearAllResetBtn');
  const resetConfirmInput = document.getElementById('resetConfirmInput');
  const confirmResetBtn = document.getElementById('confirmResetBtn');
  const resetConfirmError = document.getElementById('resetConfirmError');
  const resetItemsList = document.getElementById('resetItemsList');

  // Item descriptions for the confirmation modal
  const itemDescriptions = {
    settings: 'Settings & API Keys',
    agents: 'All Agents',
    sessions: 'Database (Sessions & Workspaces)',
    plugins: 'Plugins',
    onboarding: 'Onboarding State'
  };

  // Update reset button state based on checkbox selection
  function updateResetButtonState() {
    const anyChecked = Object.values(resetCheckboxes).some(cb => cb && cb.checked);
    if (resetAppBtn) {
      resetAppBtn.disabled = !anyChecked;
    }
  }

  // Add change listeners to all checkboxes
  Object.values(resetCheckboxes).forEach(checkbox => {
    if (checkbox) {
      checkbox.addEventListener('change', updateResetButtonState);
    }
  });

  // Select All button
  selectAllBtn?.addEventListener('click', function() {
    Object.values(resetCheckboxes).forEach(cb => {
      if (cb) cb.checked = true;
    });
    updateResetButtonState();
  });

  // Clear All button
  clearAllBtn?.addEventListener('click', function() {
    Object.values(resetCheckboxes).forEach(cb => {
      if (cb) cb.checked = false;
    });
    updateResetButtonState();
  });

  // Get selected items
  function getSelectedItems() {
    const selected = {};
    Object.entries(resetCheckboxes).forEach(([key, cb]) => {
      if (cb && cb.checked) {
        selected[key] = true;
      }
    });
    return selected;
  }

  // Open confirmation modal
  resetAppBtn?.addEventListener('click', function() {
    const selected = getSelectedItems();
    const selectedKeys = Object.keys(selected);

    if (selectedKeys.length === 0) {
      return;
    }

    // Populate the items list in the modal (using safe DOM methods)
    if (resetItemsList) {
      resetItemsList.innerHTML = '';
      selectedKeys.forEach(key => {
        const li = document.createElement('li');
        li.textContent = itemDescriptions[key];
        resetItemsList.appendChild(li);
      });
    }

    // Reset the confirmation input
    if (resetConfirmInput) {
      resetConfirmInput.value = '';
    }
    if (confirmResetBtn) {
      confirmResetBtn.disabled = true;
    }
    if (resetConfirmError) {
      resetConfirmError.style.display = 'none';
    }

    // Show the modal
    const modal = new bootstrap.Modal(document.getElementById('resetConfirmModal'));
    modal.show();
  });

  // Validate confirmation input
  resetConfirmInput?.addEventListener('input', function() {
    const isValid = this.value.trim() === 'RESET';
    if (confirmResetBtn) {
      confirmResetBtn.disabled = !isValid;
    }
    if (resetConfirmError) {
      resetConfirmError.style.display = 'none';
    }
  });

  // Handle Enter key in confirmation input
  resetConfirmInput?.addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && this.value.trim() === 'RESET') {
      confirmResetBtn?.click();
    }
  });

  // Perform the reset
  confirmResetBtn?.addEventListener('click', async function() {
    const inputValue = resetConfirmInput?.value.trim();

    if (inputValue !== 'RESET') {
      if (resetConfirmError) {
        resetConfirmError.style.display = 'block';
      }
      return;
    }

    const selected = getSelectedItems();
    // Add confirmation field for server-side validation
    selected.confirmation = 'RESET';

    // Disable button and show loading state
    this.disabled = true;
    const originalHTML = this.innerHTML;
    this.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Resetting...';

    try {
      const response = await fetch('/api/reset', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Requested-With': 'XMLHttpRequest'  // CSRF protection
        },
        body: JSON.stringify(selected)
      });

      const result = await response.json();

      if (response.ok && result.success) {
        // Close the modal
        const modal = bootstrap.Modal.getInstance(document.getElementById('resetConfirmModal'));
        modal?.hide();

        // Show success message
        const resetItems = result.reset_items.map(key => itemDescriptions[key] || key).join(', ');

        // Check if onboarding was reset - redirect to root to trigger onboarding
        if (selected.onboarding) {
          notify(`Successfully reset: ${resetItems}. Redirecting to setup wizard...`, 'success');
          setTimeout(() => { window.location.href = '/'; }, 1500);
        } else {
          notify(`Successfully reset: ${resetItems}. Reloading...`, 'success');
          setTimeout(() => { window.location.reload(); }, 1500);
        }
      } else {
        // Show error
        const errors = result.errors ? result.errors.join(', ') : 'Unknown error occurred';
        notify(`Reset failed: ${errors}`, 'error');
      }
    } catch (error) {
      console.error('Error during reset:', error);
      notify('Error during reset: ' + error.message, 'error');
    } finally {
      this.disabled = false;
      this.innerHTML = originalHTML;
    }
  });

  // Reset modal state when hidden
  document.getElementById('resetConfirmModal')?.addEventListener('hidden.bs.modal', function() {
    if (resetConfirmInput) {
      resetConfirmInput.value = '';
    }
    if (confirmResetBtn) {
      confirmResetBtn.disabled = true;
    }
    if (resetConfirmError) {
      resetConfirmError.style.display = 'none';
    }
  });
})();

// System Model Settings
(function() {
  const providerSelect = document.getElementById('systemModelProvider');
  const modelSelect = document.getElementById('systemModelModel');
  const reasoningField = document.getElementById('systemModelReasoningField');
  const reasoningSelect = document.getElementById('systemModelReasoning');
  const saveBtn = document.getElementById('saveSystemModelBtn');
  const clearBtn = document.getElementById('clearSystemModelBtn');
  const alertsContainer = document.getElementById('systemModelAlerts');
  const statusIndicator = document.getElementById('systemModelStatusIndicator');
  const statusText = document.getElementById('systemModelStatusText');
  const statusDetails = document.getElementById('systemModelStatusDetails');

  // Available providers cache
  let availableProviders = [];

  // Show alert in the system model section
  function showSystemModelAlert(message, type = 'info') {
    if (!alertsContainer) return;
    alertsContainer.innerHTML = `
      <div class="alert alert-${type} alert-dismissible fade show" role="alert">
        ${message}
        <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
      </div>
    `;
  }

  // Update status indicator
  function updateStatusIndicator(configured, provider, model, reasoningEffort = '') {
    if (!statusIndicator || !statusText || !statusDetails) return;

    if (configured) {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
        </svg>
      `;
      statusText.textContent = 'System Model Configured';
      if (provider === 'codex' && reasoningEffort) {
        statusDetails.textContent = `Using ${provider}/${model} (reasoning: ${reasoningEffort})`;
      } else {
        statusDetails.textContent = `Using ${provider}/${model}`;
      }
    } else {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#ffc107">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
      `;
      statusText.textContent = 'System Model Not Configured';
      statusDetails.textContent = 'Auto-config and other AI features require a system model.';
    }
  }

  function updateReasoningVisibility(providerName, selectedEffort = '') {
    if (!reasoningField || !reasoningSelect) return;

    if (providerName === 'codex') {
      reasoningField.style.display = '';
      reasoningSelect.disabled = false;
      reasoningSelect.value = selectedEffort || reasoningSelect.value || 'medium';
      return;
    }

    reasoningField.style.display = 'none';
    reasoningSelect.disabled = true;
  }

  // Load available providers
  async function loadProviders() {
    try {
      const response = await fetch('/api/providers');
      if (!response.ok) {
        throw new Error('Failed to fetch providers');
      }
      const data = await response.json();
      availableProviders = data.providers || [];

      // Populate provider dropdown
      if (providerSelect) {
        providerSelect.innerHTML = '<option value="">Select a provider...</option>';
        availableProviders
          .filter(p => p.available)
          .forEach(provider => {
            const option = document.createElement('option');
            option.value = provider.name;
            option.textContent = provider.display_name;
            providerSelect.appendChild(option);
          });

        // Add unavailable providers as disabled options
      availableProviders
        .filter(p => !p.available)
        .forEach(provider => {
          const option = document.createElement('option');
          option.value = provider.name;
          let unavailableReason = 'CLI required';
          if (provider.requires_key) {
            unavailableReason = 'API key required';
          } else if (provider.name === 'codex') {
            unavailableReason = 'Codex CLI required';
          } else if (provider.name === 'claude_code') {
            unavailableReason = 'Claude CLI required';
          }
          option.textContent = `${provider.display_name} (${unavailableReason})`;
          option.disabled = true;
          providerSelect.appendChild(option);
        });
      }
    } catch (error) {
      console.error('Error loading providers:', error);
      showSystemModelAlert('Error loading providers: ' + error.message, 'danger');
    }
  }

  // Load current system model configuration
  async function loadSystemModel() {
    try {
      const response = await fetch('/api/settings/system-model');
      if (!response.ok) {
        throw new Error('Failed to fetch system model');
      }
      const data = await response.json();

      updateStatusIndicator(data.configured, data.provider, data.model, data.reasoning_effort || '');

      if (data.configured && providerSelect && modelSelect) {
        providerSelect.value = data.provider;
        updateReasoningVisibility(data.provider, data.reasoning_effort || '');
        await loadModelsForProvider(data.provider);
        modelSelect.value = data.model;
        updateSaveButtonState();
      } else {
        updateReasoningVisibility('');
      }
    } catch (error) {
      console.error('Error loading system model:', error);
      updateStatusIndicator(false);
      updateReasoningVisibility('');
    }
  }

  // Load models for a specific provider
  async function loadModelsForProvider(providerName) {
    if (!modelSelect) return;

    modelSelect.innerHTML = '<option value="">Loading models...</option>';
    modelSelect.disabled = true;

    if (!providerName) {
      modelSelect.innerHTML = '<option value="">Select provider first...</option>';
      return;
    }

    try {
      const response = await fetch(`/api/settings/available-models?provider=${encodeURIComponent(providerName)}`);
      if (!response.ok) {
        throw new Error('Failed to fetch models');
      }
      const data = await response.json();

      if (!data.available) {
        modelSelect.innerHTML = '<option value="">Provider not available</option>';
        return;
      }

      modelSelect.innerHTML = '<option value="">Select a model...</option>';
      const models = data.models || [];
      const claudeCodeDescriptions = {
        opus: 'Opus 4.6 · Most capable for complex work',
        sonnet: 'Sonnet 4.5 · Best for everyday tasks',
        haiku: 'Haiku 4.5 · Fastest for quick answers'
      };
      const claudeCodeRecommended = 'haiku';
      models.forEach(model => {
        const option = document.createElement('option');
        option.value = model;
        if (providerName === 'claude_code' && claudeCodeDescriptions[model]) {
          const recommended = model === claudeCodeRecommended ? ' (recommended)' : '';
          option.textContent = `${model}${recommended} — ${claudeCodeDescriptions[model]}`;
        } else {
          option.textContent = model;
        }
        modelSelect.appendChild(option);
      });

      modelSelect.disabled = false;
    } catch (error) {
      console.error('Error loading models:', error);
      modelSelect.innerHTML = '<option value="">Error loading models</option>';
    }
  }

  // Update save button state
  function updateSaveButtonState() {
    if (!saveBtn || !providerSelect || !modelSelect) return;
    const hasProvider = providerSelect.value !== '';
    const hasModel = modelSelect.value !== '';
    saveBtn.disabled = !(hasProvider && hasModel);
  }

  // Provider selection change
  providerSelect?.addEventListener('change', async function() {
    const provider = this.value;
    updateReasoningVisibility(provider);
    await loadModelsForProvider(provider);
    updateSaveButtonState();
  });

  // Model selection change
  modelSelect?.addEventListener('change', function() {
    updateSaveButtonState();
  });

  // Save system model
  saveBtn?.addEventListener('click', async function() {
    const provider = providerSelect?.value;
    const model = modelSelect?.value;
    const reasoning_effort = provider === 'codex'
      ? (reasoningSelect?.value || 'medium')
      : '';

    if (!provider || !model) {
      showSystemModelAlert('Please select both a provider and a model.', 'warning');
      return;
    }

    saveBtn.disabled = true;
    saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Saving...';

    try {
      const response = await fetch('/api/settings/system-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model, reasoning_effort })
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || 'Failed to save system model');
      }

      const data = await response.json();
      updateStatusIndicator(data.configured, data.provider, data.model, data.reasoning_effort || '');
      updateReasoningVisibility(data.provider, data.reasoning_effort || '');
      showSystemModelAlert('System model saved successfully!', 'success');

      // Notify navbar to update system model display
      if (typeof EventBus !== 'undefined') {
        EventBus.emit('systemModel:changed', { provider, model });
      }
    } catch (error) {
      console.error('Error saving system model:', error);
      showSystemModelAlert('Error saving system model: ' + error.message, 'danger');
    } finally {
      saveBtn.disabled = false;
      saveBtn.innerHTML = `
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M15,9H5V5H15M12,19A3,3 0 0,1 9,16A3,3 0 0,1 12,13A3,3 0 0,1 15,16A3,3 0 0,1 12,19M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3Z"/>
        </svg>
        Save System Model
      `;
      updateSaveButtonState();
    }
  });

  // Clear system model
  clearBtn?.addEventListener('click', async function() {
    if (!confirm('Are you sure you want to clear the system model configuration? Auto-config and other AI features will not work without it.')) {
      return;
    }

    clearBtn.disabled = true;

    try {
      const response = await fetch('/api/settings/system-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider: '', model: '' })
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || 'Failed to clear system model');
      }

      // Reset dropdowns
      if (providerSelect) providerSelect.value = '';
      if (modelSelect) {
        modelSelect.innerHTML = '<option value="">Select provider first...</option>';
        modelSelect.disabled = true;
      }
      if (reasoningSelect) {
        reasoningSelect.value = 'medium';
      }
      updateReasoningVisibility('');

      updateStatusIndicator(false);
      updateSaveButtonState();
      showSystemModelAlert('System model cleared.', 'info');

      // Notify navbar to update system model display
      if (typeof EventBus !== 'undefined') {
        EventBus.emit('systemModel:changed', { provider: '', model: '' });
      }
    } catch (error) {
      console.error('Error clearing system model:', error);
      showSystemModelAlert('Error clearing system model: ' + error.message, 'danger');
    } finally {
      clearBtn.disabled = false;
    }
  });

  // Initialize on page load
  if (document.getElementById('systemModelProvider')) {
    loadProviders().then(() => loadSystemModel());
  }
})();

// Voice Settings (local storage for now)
(function() {
  const providerSelect = document.getElementById('speechProviderSelect');
  const languageSelect = document.getElementById('speechLanguageSelect');
  const saveBtn = document.getElementById('saveSpeechSettingsBtn');
  const diagnosticsBtn = document.getElementById('voiceDiagnosticsBtn');
  const diagnosticsResult = document.getElementById('voiceDiagnosticsResult');
  const storageKey = 'voiceSettings';

  if (!providerSelect || !languageSelect || !saveBtn) {
    return;
  }

  function notify(message, type = 'success') {
    if (window.Toast && typeof Toast[type] === 'function') {
      Toast[type](message);
      return;
    }
    alert(message);
  }

  function applySettings(settings) {
    if (!settings) return;
    if (settings.speech_provider || settings.provider) {
      providerSelect.value = settings.speech_provider || settings.provider;
    }
    if (settings.speech_language || settings.language) {
      languageSelect.value = settings.speech_language || settings.language;
    }
  }

  function cacheSettings(settings) {
    if (!window.localStorage) return;
    try {
      localStorage.setItem(storageKey, JSON.stringify({
        provider: settings.speech_provider || settings.provider,
        language: settings.speech_language || settings.language
      }));
    } catch (error) {
      console.error('Failed to cache voice settings:', error);
    }
  }

  async function loadSettings() {
    let loaded = false;
    try {
      const response = await fetch('/api/settings/speech');
      if (response.ok) {
        const data = await response.json();
        applySettings(data);
        cacheSettings(data);
        loaded = true;
      }
    } catch (error) {
      console.error('Failed to load speech settings from server:', error);
    }

    if (!loaded && window.localStorage) {
      try {
        const raw = localStorage.getItem(storageKey);
        if (!raw) return;
        const parsed = JSON.parse(raw);
        applySettings(parsed);
      } catch (error) {
        console.error('Failed to load cached voice settings:', error);
      }
    }
  }

  async function saveSettings() {
    const payload = {
      speech_provider: providerSelect.value,
      speech_language: languageSelect.value,
      speech_model: ''
    };

    try {
      const response = await fetch('/api/settings/speech', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to save settings');
      }

      cacheSettings(payload);
      notify('Voice settings saved.', 'success');
      window.dispatchEvent(new CustomEvent('voice-settings:updated', { detail: payload }));
    } catch (error) {
      console.error('Failed to save voice settings:', error);
      notify('Failed to save voice settings.', 'error');
    }
  }

  saveBtn.addEventListener('click', () => {
    saveSettings();
  });

  async function runDiagnostics() {
    if (!diagnosticsResult) return;
    diagnosticsResult.style.display = 'block';
    diagnosticsResult.textContent = 'Running voice diagnostics...';

    const speechSupported = Boolean(window.SpeechRecognition || window.webkitSpeechRecognition);
    const mediaSupported = Boolean(window.MediaRecorder);
    const deviceSupported = Boolean(navigator.mediaDevices && navigator.mediaDevices.getUserMedia);

    let micStatus = 'unknown';
    if (deviceSupported) {
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        micStatus = 'granted';
        stream.getTracks().forEach((track) => track.stop());
      } catch (error) {
        if (error && error.name === 'NotAllowedError') {
          micStatus = 'denied';
        } else {
          micStatus = `error (${error.name || 'unknown'})`;
        }
      }
    } else {
      micStatus = 'unavailable';
    }

    const lines = [
      `Web Speech API: ${speechSupported ? 'supported' : 'not supported'}`,
      `MediaRecorder: ${mediaSupported ? 'supported' : 'not supported'}`,
      `Microphone access: ${micStatus}`
    ];

    diagnosticsResult.textContent = lines.join('\n');
  }

  diagnosticsBtn?.addEventListener('click', () => {
    runDiagnostics();
  });

  loadSettings();
})();

// Display Density Settings (local storage)
(function() {
  const storageKey = 'ori-ui-density';
  const previewEl = document.getElementById('uiDensityPreview');
  const radioInputs = Array.from(document.querySelectorAll('input[name="uiDensityMode"]'));

  if (!radioInputs.length) {
    return;
  }

  const validModes = new Set(['auto', 'compact', 'roomy']);

  function readStoredMode() {
    try {
      const stored = localStorage.getItem(storageKey);
      if (stored === 'compact' || stored === 'roomy') {
        return stored;
      }
    } catch (error) {
      console.error('Failed to read UI density setting:', error);
    }
    return 'auto';
  }

  function writeStoredMode(mode) {
    try {
      if (mode === 'compact' || mode === 'roomy') {
        localStorage.setItem(storageKey, mode);
      } else {
        localStorage.removeItem(storageKey);
      }
    } catch (error) {
      console.error('Failed to persist UI density setting:', error);
    }
  }

  function updatePreview(mode) {
    if (!previewEl) return;

    const labels = {
      auto: 'Auto: Responsive spacing and typography.',
      compact: 'Compact: Tighter layout with reduced spacing.',
      roomy: 'Roomy: Expanded spacing with larger typography.'
    };
    previewEl.textContent = labels[mode] || labels.auto;
  }

  function refreshOptionState() {
    document.querySelectorAll('.ui-density-option').forEach((optionEl) => {
      const input = optionEl.querySelector('input[name="uiDensityMode"]');
      optionEl.classList.toggle('is-selected', Boolean(input && input.checked));
    });
  }

  function applyDensity(mode) {
    const root = document.documentElement;
    const body = document.body;

    if (mode === 'compact' || mode === 'roomy') {
      root.setAttribute('data-ui-density', mode);
      if (body) body.setAttribute('data-ui-density', mode);
    } else {
      root.removeAttribute('data-ui-density');
      if (body) body.removeAttribute('data-ui-density');
    }

    updatePreview(mode);
    refreshOptionState();
  }

  function setMode(mode, showNotice = false) {
    const normalized = validModes.has(mode) ? mode : 'auto';
    radioInputs.forEach((input) => {
      input.checked = input.value === normalized;
    });

    writeStoredMode(normalized);
    applyDensity(normalized);

    if (showNotice) {
      const modeLabel = normalized.charAt(0).toUpperCase() + normalized.slice(1);
      notify(`Display density set to ${modeLabel}.`, 'success');
    }
  }

  radioInputs.forEach((input) => {
    input.addEventListener('change', () => {
      if (input.checked) {
        setMode(input.value, true);
      }
    });
  });

  setMode(readStoredMode(), false);
})();

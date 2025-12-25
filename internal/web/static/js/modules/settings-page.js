// Settings Page JavaScript Module

// Toggle password visibility for API keys
document.getElementById('toggleOpenaiKey')?.addEventListener('click', function() {
  const input = document.getElementById('openaiApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

document.getElementById('toggleAnthropicKey')?.addEventListener('click', function() {
  const input = document.getElementById('anthropicApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

// Save OpenAI API Key
document.getElementById('saveOpenaiKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('openaiApiKeyInput').value.trim();

  if (!apiKey) {
    alert('Please enter an API key');
    return;
  }

  if (!apiKey.startsWith('sk-')) {
    alert('Invalid API key format. OpenAI keys start with "sk-"');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ api_key: apiKey })
    });

    if (response.ok) {
      alert('OpenAI API key saved successfully!');
      document.getElementById('openaiApiKeyInput').value = '';
    } else {
      const error = await response.text();
      alert('Failed to save API key: ' + error);
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    alert('Error saving API key: ' + error.message);
  }
});

// Save Anthropic API Key
document.getElementById('saveAnthropicKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('anthropicApiKeyInput').value.trim();

  if (!apiKey) {
    alert('Please enter an API key');
    return;
  }

  if (!apiKey.startsWith('sk-ant-')) {
    alert('Invalid API key format. Anthropic keys start with "sk-ant-"');
    return;
  }

  try {
    // Note: You'll need to add an endpoint for Anthropic API key
    // For now, we can use the same endpoint with a different structure
    const response = await fetch('/api/settings', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        anthropic_api_key: apiKey
      })
    });

    if (response.ok) {
      alert('Anthropic API key saved successfully! Please restart the server for changes to take effect.');
      document.getElementById('anthropicApiKeyInput').value = '';
    } else {
      const error = await response.text();
      alert('Failed to save API key: ' + error);
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    alert('Error saving API key: ' + error.message);
  }
});

// System Diagnostics Button
document.getElementById('systemDiagnosticsBtn')?.addEventListener('click', async function() {
  try {
    const response = await fetch('/api/updates/version');
    const data = await response.json();

    let diagnosticsInfo = `
System Diagnostics
==================
Version: ${data.version || 'Unknown'}
Status: Online
    `;

    alert(diagnosticsInfo);
  } catch (error) {
    console.error('Error fetching diagnostics:', error);
    alert('Error fetching system diagnostics');
  }
});

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
    // Use alert as fallback if no toast system exists
    if (type === 'error') {
      alert('Error: ' + message);
    } else {
      alert(message);
    }
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
    sessions: 'Chat Sessions',
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
          alert(`Successfully reset: ${resetItems}\n\nThe application will now redirect to the setup wizard.`);
          window.location.href = '/';
        } else {
          alert(`Successfully reset: ${resetItems}\n\nPlease restart the application for changes to take full effect.`);
          window.location.reload();
        }
      } else {
        // Show error
        const errors = result.errors ? result.errors.join('\n') : 'Unknown error occurred';
        alert(`Reset failed:\n${errors}`);
      }
    } catch (error) {
      console.error('Error during reset:', error);
      alert('Error during reset: ' + error.message);
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

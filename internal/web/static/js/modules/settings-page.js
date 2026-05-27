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
  const browserControlProviderSelect = document.getElementById('utilityBrowserControlProvider');
  const playwrightBrowserSelect = document.getElementById('utilityPlaywrightBrowser');
  const playwrightExecutablePathInput = document.getElementById('utilityPlaywrightExecutablePath');
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

  function normalizeBrowserControlProvider(provider) {
    const normalized = String(provider || '').toLowerCase();
    switch (normalized) {
      case 'playwright':
      case 'browserbase':
      case 'puppeteer':
      case 'auto':
        return normalized;
      default:
        return 'auto';
    }
  }

  function normalizePlaywrightBrowser(browser) {
    const normalized = String(browser || '').toLowerCase();
    switch (normalized) {
      case 'chrome':
      case 'firefox':
      case 'webkit':
      case 'msedge':
      case 'brave':
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
    const browserControlProvider = normalizeBrowserControlProvider(utility.browser_control_provider);
    const playwrightBrowser = normalizePlaywrightBrowser(utility.playwright_browser);
    const playwrightExecutablePath = String(utility.playwright_executable_path || '').trim();
    const hasBraveKey = Boolean(utility.has_brave_api_key);
    const masked = utility.brave_api_key_masked || '';

    providerSelect.value = provider;
    if (browserControlProviderSelect) {
      browserControlProviderSelect.value = browserControlProvider;
    }
    if (playwrightBrowserSelect) {
      playwrightBrowserSelect.value = playwrightBrowser;
    }
    if (playwrightExecutablePathInput) {
      playwrightExecutablePathInput.value = playwrightExecutablePath;
    }
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
    const browserControlProvider = normalizeBrowserControlProvider(browserControlProviderSelect?.value);
    const playwrightBrowser = normalizePlaywrightBrowser(playwrightBrowserSelect?.value);
    const playwrightExecutablePath = (playwrightExecutablePathInput?.value || '').trim();
    setButtonLoading(saveProviderBtn, true, 'Saving...');
    try {
      await saveUtilitySettings({
        search_provider: provider,
        browser_control_provider: browserControlProvider,
        playwright_browser: playwrightBrowser,
        playwright_executable_path: playwrightExecutablePath
      });
      notify('Web tool settings saved.', 'success');
      if (provider === 'brave' && !utility?.has_brave_api_key) {
        notify('Brave Search requires an API key. Add one in "Manage API Key".', 'warning');
      }
    } catch (error) {
      console.error('Failed to save web tool settings:', error);
      notify('Failed to save web tool settings: ' + error.message, 'error');
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

// Workspace Directory Settings
(function() {
  const input = document.getElementById('workspaceRootInput');
  const browseBtn = document.getElementById('browseWorkspaceRootBtn');
  const saveBtn = document.getElementById('saveWorkspaceRootBtn');
  const resetBtn = document.getElementById('resetWorkspaceRootBtn');
  const statusIndicator = document.getElementById('workspaceRootStatusIndicator');
  const statusText = document.getElementById('workspaceRootStatusText');
  const statusDetails = document.getElementById('workspaceRootStatusDetails');

  if (!input) {
    return;
  }

  let workspaceRootState = null;

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

  function updateStatus(state) {
    if (!statusIndicator || !statusText || !statusDetails) return;

    const source = String(state?.source || 'default');
    const effectiveRoot = String(state?.effective_workspace_root || '').trim();

    if (source === 'settings') {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
        </svg>
      `;
      statusText.textContent = 'Custom workspace directory active';
      statusDetails.textContent = effectiveRoot ? `New workspaces will be created in ${effectiveRoot}.` : '';
      return;
    }

    if (source === 'environment') {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#3b82f6">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M13,17H11V11H13V17M13,9H11V7H13V9Z"/>
        </svg>
      `;
      statusText.textContent = 'Using workspace directory from WORKSPACE_DIR';
      statusDetails.textContent = effectiveRoot ? `${effectiveRoot} is active until you save a custom directory.` : '';
      return;
    }

    statusIndicator.innerHTML = `
      <svg width="24" height="24" viewBox="0 0 24 24" fill="#3b82f6">
        <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M13,17H11V11H13V17M13,9H11V7H13V9Z"/>
      </svg>
    `;
    statusText.textContent = 'Using built-in workspace directory';
    statusDetails.textContent = effectiveRoot ? `${effectiveRoot} is the current default until you save a custom directory.` : '';
  }

  function applyWorkspaceRootState(state) {
    workspaceRootState = state || {};
    const configuredRoot = String(workspaceRootState.workspace_root || '').trim();
    const effectiveRoot = String(workspaceRootState.effective_workspace_root || '').trim();
    input.value = configuredRoot || effectiveRoot;
    if (resetBtn) {
      resetBtn.disabled = !configuredRoot;
    }
    updateStatus(workspaceRootState);
  }

  async function loadWorkspaceRoot() {
    const response = await fetch('/api/settings/workspace-root');
    if (!response.ok) {
      throw new Error(await response.text() || 'Failed to load workspace directory');
    }
    const data = await response.json();
    applyWorkspaceRootState(data);
  }

  async function saveWorkspaceRoot(workspaceRoot) {
    const response = await fetch('/api/settings/workspace-root', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ workspace_root: workspaceRoot })
    });

    if (!response.ok) {
      throw new Error(await response.text() || 'Failed to save workspace directory');
    }

    const data = await response.json();
    applyWorkspaceRootState(data);
    return data;
  }

  browseBtn?.addEventListener('click', async function() {
    setButtonLoading(browseBtn, true, 'Selecting...');
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'Select Default Workspace Directory'
        })
      });

      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        throw new Error(result.error || 'Failed to open folder picker');
      }

      if (result.selected && result.path) {
        input.value = result.path;
        input.focus();
      }
    } catch (error) {
      console.error('Failed to browse workspace directory:', error);
      notify('Failed to open folder picker: ' + error.message, 'error');
    } finally {
      setButtonLoading(browseBtn, false);
    }
  });

  saveBtn?.addEventListener('click', async function() {
    setButtonLoading(saveBtn, true, 'Saving...');
    try {
      await saveWorkspaceRoot(input.value.trim());
      notify('Workspace directory saved.', 'success');
    } catch (error) {
      console.error('Failed to save workspace directory:', error);
      notify('Failed to save workspace directory: ' + error.message, 'error');
    } finally {
      setButtonLoading(saveBtn, false);
    }
  });

  resetBtn?.addEventListener('click', async function() {
    if (!workspaceRootState?.workspace_root) {
      return;
    }

    setButtonLoading(resetBtn, true, 'Clearing...');
    try {
      await saveWorkspaceRoot('');
      notify('Custom workspace directory cleared.', 'success');
    } catch (error) {
      console.error('Failed to clear workspace directory:', error);
      notify('Failed to clear workspace directory: ' + error.message, 'error');
    } finally {
      setButtonLoading(resetBtn, false);
    }
  });

  loadWorkspaceRoot().catch((error) => {
    console.error('Failed to load workspace directory:', error);
    if (statusIndicator) {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#ef4444">
          <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
        </svg>
      `;
    }
    if (statusText) {
      statusText.textContent = 'Failed to load workspace directory';
    }
    if (statusDetails) {
      statusDetails.textContent = error.message || '';
    }
  });
})();

// Vault Directory Settings
(function() {
  const input = document.getElementById('vaultRootInput');
  const browseBtn = document.getElementById('browseVaultRootBtn');
  const saveBtn = document.getElementById('saveVaultRootBtn');
  const resetBtn = document.getElementById('resetVaultRootBtn');
  const statusIndicator = document.getElementById('vaultRootStatusIndicator');
  const statusText = document.getElementById('vaultRootStatusText');
  const statusDetails = document.getElementById('vaultRootStatusDetails');

  if (!input) {
    return;
  }

  let vaultRootState = null;

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

  function updateStatus(state) {
    if (!statusIndicator || !statusText || !statusDetails) return;

    const source = String(state?.source || 'default');
    const effectiveRoot = String(state?.effective_vault_root || '').trim();

    if (source === 'settings') {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
        </svg>
      `;
      statusText.textContent = 'Custom vault directory active';
      statusDetails.textContent = effectiveRoot ? `New managed vault folders will be created in ${effectiveRoot}.` : '';
      return;
    }

    if (source === 'environment') {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#3b82f6">
          <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M13,17H11V11H13V17M13,9H11V7H13V9Z"/>
        </svg>
      `;
      statusText.textContent = 'Using vault directory from ORI_VAULT_DIR';
      statusDetails.textContent = effectiveRoot ? `${effectiveRoot} is active until you save a custom directory.` : '';
      return;
    }

    statusIndicator.innerHTML = `
      <svg width="24" height="24" viewBox="0 0 24 24" fill="#3b82f6">
        <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M13,17H11V11H13V17M13,9H11V7H13V9Z"/>
      </svg>
    `;
    statusText.textContent = 'Using built-in vault directory';
    statusDetails.textContent = effectiveRoot ? `${effectiveRoot} is the current default until you save a custom directory.` : '';
  }

  function applyVaultRootState(state) {
    vaultRootState = state || {};
    const configuredRoot = String(vaultRootState.vault_root || '').trim();
    const effectiveRoot = String(vaultRootState.effective_vault_root || '').trim();
    input.value = configuredRoot || effectiveRoot;
    if (resetBtn) {
      resetBtn.disabled = !configuredRoot;
    }
    updateStatus(vaultRootState);
  }

  async function loadVaultRoot() {
    const response = await fetch('/api/settings/vault-root');
    if (!response.ok) {
      throw new Error(await response.text() || 'Failed to load vault directory');
    }
    const data = await response.json();
    applyVaultRootState(data);
  }

  async function saveVaultRoot(vaultRoot) {
    const response = await fetch('/api/settings/vault-root', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ vault_root: vaultRoot })
    });

    if (!response.ok) {
      throw new Error(await response.text() || 'Failed to save vault directory');
    }

    const data = await response.json();
    applyVaultRootState(data);
    return data;
  }

  browseBtn?.addEventListener('click', async function() {
    setButtonLoading(browseBtn, true, 'Selecting...');
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'Select Default Vault Directory'
        })
      });

      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        throw new Error(result.error || 'Failed to open folder picker');
      }

      if (result.selected && result.path) {
        input.value = result.path;
        input.focus();
      }
    } catch (error) {
      console.error('Failed to browse vault directory:', error);
      notify('Failed to open folder picker: ' + error.message, 'error');
    } finally {
      setButtonLoading(browseBtn, false);
    }
  });

  saveBtn?.addEventListener('click', async function() {
    setButtonLoading(saveBtn, true, 'Saving...');
    try {
      await saveVaultRoot(input.value.trim());
      notify('Vault directory saved.', 'success');
    } catch (error) {
      console.error('Failed to save vault directory:', error);
      notify('Failed to save vault directory: ' + error.message, 'error');
    } finally {
      setButtonLoading(saveBtn, false);
    }
  });

  resetBtn?.addEventListener('click', async function() {
    if (!vaultRootState?.vault_root) {
      return;
    }

    setButtonLoading(resetBtn, true, 'Clearing...');
    try {
      await saveVaultRoot('');
      notify('Custom vault directory cleared.', 'success');
    } catch (error) {
      console.error('Failed to clear vault directory:', error);
      notify('Failed to clear vault directory: ' + error.message, 'error');
    } finally {
      setButtonLoading(resetBtn, false);
    }
  });

  loadVaultRoot().catch((error) => {
    console.error('Failed to load vault directory:', error);
    if (statusIndicator) {
      statusIndicator.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#ef4444">
          <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
        </svg>
      `;
    }
    if (statusText) {
      statusText.textContent = 'Failed to load vault directory';
    }
    if (statusDetails) {
      statusDetails.textContent = error.message || '';
    }
  });
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
      const settingsResp = await fetch('/api/settings/session');
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
      const response = await fetch('/api/settings/session', {
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

  function normalizeModelOptions(rawModelOptions, fallbackModels) {
    if (Array.isArray(rawModelOptions) && rawModelOptions.length > 0) {
      return rawModelOptions
        .filter(option => option && typeof option.id === 'string' && option.id.length > 0)
        .map(option => ({
          id: option.id,
          label: (typeof option.label === 'string' && option.label.length > 0) ? option.label : option.id,
          description: (typeof option.description === 'string') ? option.description : '',
          recommended: Boolean(option.recommended)
        }));
    }

    const models = Array.isArray(fallbackModels) ? fallbackModels : [];
    return models
      .filter(model => typeof model === 'string' && model.length > 0)
      .map(model => ({
        id: model,
        label: model,
        description: '',
        recommended: false
      }));
  }

  function formatModelOptionText(modelOption) {
    const recommended = modelOption.recommended ? ' (recommended)' : '';
    if (modelOption.description) {
      return `${modelOption.label}${recommended} — ${modelOption.description}`;
    }
    return `${modelOption.label}${recommended}`;
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
          } else if (provider.name === 'lmstudio') {
            unavailableReason = 'Start LM Studio server';
          } else if (provider.name === 'mlx_lm') {
            unavailableReason = 'Start mlx_lm.server';
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
      const modelOptions = normalizeModelOptions(data.model_options, data.models);
      modelOptions.forEach(modelOption => {
        const option = document.createElement('option');
        option.value = modelOption.id;
        option.textContent = formatModelOptionText(modelOption);
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

// Private Vault Settings
(function() {
  const DEFAULT_VAULT_ID = '';
  const VAULT_STORAGE_KEY = 'ori-selected-vault-id';
  const section = document.getElementById('private-vault');
  if (!section) {
    return;
  }

  const statusIndicator = document.getElementById('vaultStatusIndicator');
  const statusText = document.getElementById('vaultStatusText');
  const statusDetails = document.getElementById('vaultStatusDetails');
  const backendLabel = document.getElementById('vaultBackendLabel');
  const recordCountLabel = document.getElementById('vaultRecordCount');
  const writableLabel = document.getElementById('vaultWritableLabel');
  const modeLabel = document.getElementById('vaultModeLabel');
  const unlockPasswordInput = document.getElementById('vaultUnlockPassword');
  const unlockBtn = document.getElementById('vaultUnlockBtn');
  const lockBtn = document.getElementById('vaultLockBtn');
  const refreshBtn = document.getElementById('vaultRefreshBtn');
  const toggleUnlockPasswordBtn = document.getElementById('toggleVaultUnlockPassword');
  const unlockPasswordHelp = document.getElementById('vaultUnlockPasswordHelp');
  const alertsEl = document.getElementById('vaultAlerts');
  const activeVaultSelect = document.getElementById('vaultActiveVaultId');
  const activeVaultMeta = document.getElementById('vaultActiveVaultMeta');
  const editVaultNameInput = document.getElementById('vaultEditVaultName');
  const editVaultDescriptionInput = document.getElementById('vaultEditVaultDescription');
  const renameVaultBtn = document.getElementById('vaultRenameVaultBtn');
  const deleteVaultBtn = document.getElementById('vaultDeleteVaultBtn');
  const newVaultNameInput = document.getElementById('vaultNewVaultName');
  const newVaultDescriptionInput = document.getElementById('vaultNewVaultDescription');
  const newVaultPasswordInput = document.getElementById('vaultNewVaultPassword');
  const confirmVaultPasswordInput = document.getElementById('vaultConfirmVaultPassword');
  const createVaultSpaceBtn = document.getElementById('vaultCreateVaultSpaceBtn');

  const entryTypeInput = document.getElementById('vaultEntryType');
  const entryWorkspaceInput = document.getElementById('vaultEntryWorkspaceId');
  const entryLabelInput = document.getElementById('vaultEntryLabel');
  const entryTagsInput = document.getElementById('vaultEntryTags');
  const entryRetentionInput = document.getElementById('vaultEntryRetention');
  const entryPayloadInput = document.getElementById('vaultEntryPayload');
  const saveEntryBtn = document.getElementById('vaultSaveEntryBtn');
  const resetEntryBtn = document.getElementById('vaultResetEntryBtn');
  const revealPayloadBtn = document.getElementById('vaultRevealPayloadBtn');
  const deleteEntryBtn = document.getElementById('vaultDeleteEntryBtn');
  const recordsListEl = document.getElementById('vaultRecordsList');
  const selectionBadge = document.getElementById('vaultSelectionBadge');

  const grantWorkspaceInput = document.getElementById('vaultGrantWorkspaceId');
  const grantActorTypeInput = document.getElementById('vaultGrantActorType');
  const grantActorIDInput = document.getElementById('vaultGrantActorId');
  const grantCapabilityInput = document.getElementById('vaultGrantCapability');
  const grantRecordTypeInput = document.getElementById('vaultGrantRecordType');
  const createGrantBtn = document.getElementById('vaultCreateGrantBtn');
  const grantsListEl = document.getElementById('vaultGrantsList');

  const exportWorkspaceInput = document.getElementById('vaultExportWorkspaceId');
  const exportConfirmInput = document.getElementById('vaultExportConfirm');
  const exportPasswordInput = document.getElementById('vaultExportPassword');
  const exportBtn = document.getElementById('vaultExportBtn');
  const importChooseFileBtn = document.getElementById('vaultImportChooseFileBtn');
  const importFileInput = document.getElementById('vaultImportFile');
  const importFileName = document.getElementById('vaultImportFileName');
  const importPasswordInput = document.getElementById('vaultImportPassword');
  const importModeInput = document.getElementById('vaultImportMode');
  const importModeCurrentOption = document.getElementById('vaultImportModeCurrentOption');
  const importCurrentHint = document.getElementById('vaultImportCurrentHint');
  const importCreateFields = document.getElementById('vaultImportCreateFields');
  const importVaultNameInput = document.getElementById('vaultImportVaultName');
  const importVaultDescriptionInput = document.getElementById('vaultImportVaultDescription');
  const importVaultPasswordInput = document.getElementById('vaultImportVaultPassword');
  const importConfirmVaultPasswordInput = document.getElementById('vaultImportConfirmVaultPassword');
  const importRestoreGrantsInput = document.getElementById('vaultImportRestoreGrants');
  const importBtn = document.getElementById('vaultImportBtn');

  let vaultStatus = null;
  let vaults = [];
  let selectedVaultID = DEFAULT_VAULT_ID;
  let records = [];
  let grants = [];
  let selectedRecord = null;
  let payloadRevealed = false;

  function setButtonLoading(button, loading, loadingLabel) {
    if (!button) return;
    if (loading) {
      button.dataset.originalLabel = button.innerHTML;
      button.disabled = true;
      button.innerHTML = `<span class="spinner-border spinner-border-sm me-2"></span>${loadingLabel}`;
      return;
    }

    button.disabled = false;
    if (button.dataset.originalLabel) {
      button.innerHTML = button.dataset.originalLabel;
    }
  }

  function escapeHTML(value) {
    return String(value || '')
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  function readStoredVaultID() {
    try {
      return String(window.localStorage.getItem(VAULT_STORAGE_KEY) || '').trim();
    } catch (error) {
      return '';
    }
  }

  function writeStoredVaultID(vaultID) {
    try {
      if (vaultID) {
        window.localStorage.setItem(VAULT_STORAGE_KEY, vaultID);
      } else {
        window.localStorage.removeItem(VAULT_STORAGE_KEY);
      }
    } catch (error) {
      console.error('Failed to persist selected vault:', error);
    }
  }

  function currentVaultID() {
    return String(selectedVaultID || '').trim();
  }

  function currentVault() {
    return vaults.find((item) => item.id === currentVaultID()) || null;
  }

  function vaultRecordCount(vaultItem) {
    const count = Number(vaultItem?.record_count || 0);
    return Number.isFinite(count) ? count : 0;
  }

  function vaultDisplayLabel(vaultItem) {
    if (!vaultItem) {
      return 'Vault';
    }
    return `${String(vaultItem.name || 'Vault')}`;
  }

  function canImportIntoCurrentVault() {
    return Boolean(currentVaultID() && vaultStatus?.available && !vaultStatus?.locked);
  }

  function vaultURL(path, vaultID = currentVaultID()) {
    const normalizedVaultID = String(vaultID || '').trim();
    if (!normalizedVaultID) {
      return path;
    }

    const separator = path.includes('?') ? '&' : '?';
    return `${path}${separator}vault_id=${encodeURIComponent(normalizedVaultID)}`;
  }

  async function apiRequest(url, options = {}) {
    const config = {
      method: options.method || 'GET',
      headers: {
        'Content-Type': 'application/json',
        ...(options.headers || {})
      }
    };

    if (options.body !== undefined && config.method !== 'GET') {
      config.body = JSON.stringify(options.body);
    }

    const response = await fetch(url, config);
    const contentType = response.headers.get('content-type') || '';
    let data = null;

    if (contentType.includes('application/json')) {
      data = await response.json();
    } else {
      const text = await response.text();
      data = text ? { error: text } : {};
    }

    if (!response.ok) {
      throw new Error(data?.error || response.statusText || 'Request failed');
    }

    return data;
  }

  function readFileAsText(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();

      reader.onload = () => {
        resolve(String(reader.result || ''));
      };

      reader.onerror = () => {
        reject(new Error(`Failed to read file "${String(file?.name || 'import bundle')}".`));
      };

      reader.readAsText(file);
    });
  }

  function parseTags(rawValue) {
    return String(rawValue || '')
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean);
  }

  function prettyPayload(payload) {
    if (payload === undefined || payload === null) {
      return '{}';
    }
    try {
      return JSON.stringify(payload, null, 2);
    } catch (error) {
      console.error('Failed to pretty-print vault payload:', error);
      return '{}';
    }
  }

  function parsePayloadInput() {
    const raw = String(entryPayloadInput?.value || '').trim();
    if (!raw) {
      return {};
    }

    try {
      return JSON.parse(raw);
    } catch (error) {
      throw new Error('Payload must be valid JSON');
    }
  }

  function backendLabelFor(status) {
    const backend = String(status?.secret_store?.backend || 'unknown');
    switch (backend) {
      case 'vault_password':
        return 'Vault Password';
      case 'darwin_keychain':
        return 'macOS Keychain';
      case 'linux_secret_service':
        return 'Linux Secret Service';
      case 'windows_secure_store':
        return 'Windows Secure Store';
      case 'passphrase_fallback':
        return 'Passphrase Fallback';
      case 'unavailable':
        return 'Unavailable';
      default:
        return backend.replaceAll('_', ' ');
    }
  }

  function statusDot(color) {
    return `<span style="display:inline-block;width:12px;height:12px;border-radius:999px;background:${color};"></span>`;
  }

  function showInlineAlert(message, type = 'info') {
    if (!alertsEl) {
      notify(message, type);
      return;
    }

    const classes = {
      success: 'alert-success',
      error: 'alert-danger',
      warning: 'alert-warning',
      info: 'alert-info'
    };

    alertsEl.innerHTML = `
      <div class="alert ${classes[type] || classes.info} mb-0" role="alert">
        ${escapeHTML(message)}
      </div>
    `;

    window.clearTimeout(showInlineAlert.timeoutId);
    showInlineAlert.timeoutId = window.setTimeout(() => {
      alertsEl.innerHTML = '';
    }, 5000);
  }

  function syncImportControls() {
    const canImportCurrent = canImportIntoCurrentVault();

    if (importModeCurrentOption) {
      importModeCurrentOption.disabled = !canImportCurrent;
      importModeCurrentOption.hidden = !canImportCurrent;
    }

    if (importModeInput) {
      if (!canImportCurrent && importModeInput.value === 'current') {
        importModeInput.value = 'new';
      }
      if (!importModeInput.value) {
        importModeInput.value = 'new';
      }
    }

    const mode = importModeInput?.value === 'current' && canImportCurrent ? 'current' : 'new';
    if (importCurrentHint) {
      importCurrentHint.hidden = mode !== 'current';
      importCurrentHint.textContent = mode === 'current'
        ? `The selected vault "${vaultDisplayLabel(currentVault())}" is unlocked and ready for imported records.`
        : 'The selected vault must already be unlocked before Ori can import records into it.';
    }

    if (importCreateFields) {
      importCreateFields.hidden = mode !== 'new';
    }
  }

  function renderVaultSpaces() {
    if (!activeVaultSelect || !activeVaultMeta) {
      return;
    }

    if (!vaults.length) {
      activeVaultSelect.innerHTML = '<option value="">No vaults available</option>';
      activeVaultMeta.textContent = 'Create a vault to begin storing encrypted records.';
      if (editVaultNameInput) editVaultNameInput.value = '';
      if (editVaultDescriptionInput) editVaultDescriptionInput.value = '';
      if (renameVaultBtn) renameVaultBtn.disabled = true;
      if (deleteVaultBtn) deleteVaultBtn.disabled = true;
      syncImportControls();
      return;
    }

    activeVaultSelect.innerHTML = vaults.map((item) => {
      const selected = item.id === currentVaultID() ? ' selected' : '';
      const label = `${vaultDisplayLabel(item)} · ${vaultRecordCount(item)}`;
      return `<option value="${escapeHTML(item.id)}"${selected}>${escapeHTML(label)}</option>`;
    }).join('');

    const selectedVault = currentVault();
    if (!selectedVault) {
      activeVaultMeta.textContent = 'Select a vault to continue.';
      if (editVaultNameInput) editVaultNameInput.value = '';
      if (editVaultDescriptionInput) editVaultDescriptionInput.value = '';
      if (renameVaultBtn) renameVaultBtn.disabled = true;
      if (deleteVaultBtn) deleteVaultBtn.disabled = true;
      syncImportControls();
      return;
    }

    const details = [];
    if (selectedVault.description) {
      details.push(selectedVault.description);
    }
    details.push(selectedVault.password_protected ? 'Own password' : 'Legacy vault');
    details.push(`${vaultRecordCount(selectedVault)} stored ${vaultRecordCount(selectedVault) === 1 ? 'entry' : 'entries'}`);
    activeVaultMeta.textContent = details.join(' • ');

    if (editVaultNameInput) {
      editVaultNameInput.value = selectedVault.name || '';
      editVaultNameInput.disabled = false;
    }
    if (editVaultDescriptionInput) {
      editVaultDescriptionInput.value = selectedVault.description || '';
      editVaultDescriptionInput.disabled = false;
    }
    if (renameVaultBtn) {
      renameVaultBtn.disabled = false;
    }
    if (deleteVaultBtn) {
      deleteVaultBtn.disabled = false;
      deleteVaultBtn.title = '';
    }
    syncImportControls();
  }

  async function loadVaults() {
    const data = await apiRequest('/api/vault/vaults');
    vaults = Array.isArray(data.vaults) ? data.vaults : [];

    const preferredVaultID = String(selectedVaultID || readStoredVaultID() || '').trim();
    const nextVault = vaults.find((item) => item.id === preferredVaultID)
      || vaults[0]
      || null;

    selectedVaultID = nextVault?.id || DEFAULT_VAULT_ID;
    writeStoredVaultID(selectedVaultID);
    renderVaultSpaces();
  }

  function switchVault(nextVaultID) {
    nextVaultID = String(nextVaultID || '').trim();
    if (!nextVaultID || nextVaultID === currentVaultID()) {
      renderVaultSpaces();
      return;
    }

    selectedVaultID = nextVaultID;
    writeStoredVaultID(selectedVaultID);
    clearRecordForm();
    refreshVault();
  }

  async function createVaultSpace() {
    const name = String(newVaultNameInput?.value || '').trim();
    const description = String(newVaultDescriptionInput?.value || '').trim();
    const password = String(newVaultPasswordInput?.value || '');
    const confirmPassword = String(confirmVaultPasswordInput?.value || '');

    if (!name) {
      showInlineAlert('New vault name is required before creating a vault.', 'warning');
      return;
    }
    if (!password.trim()) {
      showInlineAlert('A vault password is required before creating a vault.', 'warning');
      return;
    }
    if (password !== confirmPassword) {
      showInlineAlert('Vault password confirmation does not match.', 'warning');
      return;
    }

    try {
      setButtonLoading(createVaultSpaceBtn, true, 'Creating vault');
      const response = await apiRequest('/api/vault/vaults', {
        method: 'POST',
        body: {
          name,
          description,
          vault_password: password
        }
      });

      if (newVaultNameInput) newVaultNameInput.value = '';
      if (newVaultDescriptionInput) newVaultDescriptionInput.value = '';
      if (newVaultPasswordInput) newVaultPasswordInput.value = '';
      if (confirmVaultPasswordInput) confirmVaultPasswordInput.value = '';

      await loadVaults();
      if (response?.vault?.id) {
        selectedVaultID = response.vault.id;
        writeStoredVaultID(selectedVaultID);
        renderVaultSpaces();
      }
      clearRecordForm();
      notify('Vault created.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to create vault:', error);
      showInlineAlert(error.message || 'Failed to create vault.', 'error');
    } finally {
      setButtonLoading(createVaultSpaceBtn, false);
    }
  }

  async function updateVaultSpace() {
    const selectedVault = currentVault();
    if (!selectedVault) {
      showInlineAlert('Select a vault before updating its details.', 'warning');
      return;
    }

    const name = String(editVaultNameInput?.value || '').trim();
    const description = String(editVaultDescriptionInput?.value || '').trim();

    if (!name) {
      showInlineAlert('Vault name is required before saving changes.', 'warning');
      return;
    }

    try {
      setButtonLoading(renameVaultBtn, true, 'Saving vault');
      await apiRequest(`/api/vault/vaults/${encodeURIComponent(selectedVault.id)}`, {
        method: 'PATCH',
        body: {
          name,
          description
        }
      });
      notify('Vault details updated.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to update vault:', error);
      showInlineAlert(error.message || 'Failed to update vault.', 'error');
    } finally {
      setButtonLoading(renameVaultBtn, false);
    }
  }

  async function deleteVaultSpace() {
    const selectedVault = currentVault();
    if (!selectedVault) {
      showInlineAlert('Select a vault before deleting it.', 'warning');
      return;
    }
    const recordCount = vaultRecordCount(selectedVault);
    const confirmed = window.confirm(
      `Delete vault "${selectedVault.name}"?${recordCount > 0 ? ` This will permanently remove ${recordCount} encrypted ${recordCount === 1 ? 'entry' : 'entries'}.` : ''}`
    );
    if (!confirmed) {
      return;
    }

    try {
      setButtonLoading(deleteVaultBtn, true, 'Deleting vault');
      await apiRequest(`/api/vault/vaults/${encodeURIComponent(selectedVault.id)}`, {
        method: 'DELETE'
      });
      selectedVaultID = DEFAULT_VAULT_ID;
      writeStoredVaultID(selectedVaultID);
      clearRecordForm();
      notify('Vault deleted.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to delete vault:', error);
      showInlineAlert(error.message || 'Failed to delete vault.', 'error');
    } finally {
      setButtonLoading(deleteVaultBtn, false);
    }
  }

  function setInteractiveState(locked) {
    const disableVaultEditing = !vaultStatus || !vaultStatus.available || Boolean(locked);

    saveEntryBtn.disabled = disableVaultEditing;
    resetEntryBtn.disabled = false;
    exportBtn.disabled = disableVaultEditing;
    lockBtn.disabled = !vaultStatus || !vaultStatus.available || vaultStatus.locked;
    unlockBtn.disabled = !vaultStatus || !vaultStatus.available || Boolean(vaultStatus && !vaultStatus.locked && !vaultStatus.requires_passphrase);
    revealPayloadBtn.disabled = disableVaultEditing || !selectedRecord;
    deleteEntryBtn.disabled = disableVaultEditing || !selectedRecord;

    if (disableVaultEditing) {
      recordsListEl.innerHTML = vaultStatus?.available
        ? '<div class="settings-help">Unlock the vault to browse saved entries.</div>'
        : '<div class="settings-help">Create a vault to begin storing encrypted entries.</div>';
      clearRecordForm({ preserveStatus: true });
    }
  }

  function renderStatus(state) {
    vaultStatus = state || null;
    if (!vaultStatus) {
      return;
    }

    if (!vaultStatus.available) {
      statusIndicator.innerHTML = statusDot('#94a3b8');
      statusText.textContent = 'No vault selected';
      statusDetails.textContent = vaultStatus.message || 'Create a vault to begin storing encrypted records.';
      backendLabel.textContent = backendLabelFor(vaultStatus);
      recordCountLabel.textContent = '0';
      writableLabel.textContent = 'Unavailable';
      modeLabel.textContent = 'Create a vault';
      modeLabel.style.background = '#e2e8f0';
      modeLabel.style.color = '#475569';
      if (unlockPasswordHelp) {
        unlockPasswordHelp.textContent = 'Per-vault passwords are required for new vaults. Legacy vaults may still unlock through secure system storage or the older fallback passphrase flow.';
      }
      setInteractiveState(true);
      syncImportControls();
      return;
    }

    const locked = Boolean(vaultStatus.locked);
    const requiresPassphrase = Boolean(vaultStatus.requires_passphrase);
    const passwordProtected = Boolean(vaultStatus.password_protected);
    const dotColor = locked ? '#f59e0b' : '#10b981';
    const selectedVault = currentVault();
    const vaultName = selectedVault?.name || vaultStatus.vault_name || 'Vault';
    statusIndicator.innerHTML = statusDot(dotColor);
    statusText.textContent = locked ? `${vaultName} locked` : `${vaultName} available`;

    const detailParts = [];
    if (selectedVault?.description) {
      detailParts.push(selectedVault.description);
    }
    if (vaultStatus.message) {
      detailParts.push(vaultStatus.message);
    }
    detailParts.push(`Record count: ${vaultStatus.record_count ?? 0}`);
    statusDetails.textContent = detailParts.join(' • ');

    backendLabel.textContent = backendLabelFor(vaultStatus);
    recordCountLabel.textContent = String(vaultStatus.record_count ?? 0);
    writableLabel.textContent = vaultStatus.writable ? 'Writable' : 'Read-only / locked';

    if (passwordProtected && locked && requiresPassphrase) {
      modeLabel.textContent = 'Vault password required';
      modeLabel.style.background = '#fef3c7';
      modeLabel.style.color = '#92400e';
    } else if (passwordProtected) {
      modeLabel.textContent = 'Per-vault password';
      modeLabel.style.background = '#dcfce7';
      modeLabel.style.color = '#166534';
    } else if (locked && requiresPassphrase) {
      modeLabel.textContent = 'Legacy passphrase required';
      modeLabel.style.background = '#fef3c7';
      modeLabel.style.color = '#92400e';
    } else if (String(vaultStatus.secret_store?.backend || '') === 'passphrase_fallback') {
      modeLabel.textContent = 'Passphrase fallback active';
      modeLabel.style.background = '#dbeafe';
      modeLabel.style.color = '#1d4ed8';
    } else {
      modeLabel.textContent = 'OS secure storage';
      modeLabel.style.background = '#dcfce7';
      modeLabel.style.color = '#166534';
    }

    if (unlockPasswordHelp) {
      unlockPasswordHelp.textContent = passwordProtected
        ? 'Enter the password for the selected vault to unlock it.'
        : 'This legacy vault may still unlock through secure system storage or the older fallback passphrase flow.';
    }

    setInteractiveState(locked);
    syncImportControls();
  }

  function clearRecordForm(options = {}) {
    const preserveStatus = Boolean(options.preserveStatus);

    selectedRecord = null;
    payloadRevealed = false;
    selectionBadge.textContent = 'No selection';
    entryTypeInput.value = 'personal_note';
    entryWorkspaceInput.value = '';
    entryLabelInput.value = '';
    entryTagsInput.value = '';
    entryRetentionInput.value = '';
    entryPayloadInput.disabled = false;
    entryPayloadInput.value = '{\n  \n}';
    entryPayloadInput.style.filter = 'none';
    saveEntryBtn.textContent = 'Save To Vault';
    revealPayloadBtn.classList.add('d-none');
    deleteEntryBtn.classList.add('d-none');

    if (!preserveStatus && vaultStatus) {
      setInteractiveState(vaultStatus.locked);
    }
  }

  function applyRecordToForm(record) {
    selectedRecord = record;
    payloadRevealed = false;
    selectionBadge.textContent = record.label || record.type || 'Selected';
    entryTypeInput.value = record.type || 'personal_note';
    entryWorkspaceInput.value = record.workspace_id || '';
    entryLabelInput.value = record.label || '';
    entryTagsInput.value = Array.isArray(record.tags) ? record.tags.join(', ') : '';
    entryRetentionInput.value = record.retention_policy || '';
    entryPayloadInput.disabled = true;
    entryPayloadInput.style.filter = 'blur(6px)';
    entryPayloadInput.value = '{\n  "locked": true\n}';
    saveEntryBtn.textContent = 'Save Changes';
    revealPayloadBtn.textContent = 'Reveal Payload';
    revealPayloadBtn.classList.remove('d-none');
    deleteEntryBtn.classList.remove('d-none');
    setInteractiveState(Boolean(vaultStatus?.locked));
  }

  function togglePayloadReveal() {
    if (!selectedRecord) {
      return;
    }

    payloadRevealed = !payloadRevealed;
    if (payloadRevealed) {
      entryPayloadInput.disabled = false;
      entryPayloadInput.style.filter = 'none';
      entryPayloadInput.value = prettyPayload(selectedRecord.payload);
      revealPayloadBtn.textContent = 'Hide Payload';
      return;
    }

    entryPayloadInput.disabled = true;
    entryPayloadInput.style.filter = 'blur(6px)';
    entryPayloadInput.value = '{\n  "locked": true\n}';
    revealPayloadBtn.textContent = 'Reveal Payload';
  }

  function renderRecordsList(items) {
    records = Array.isArray(items) ? items : [];

    if (!records.length) {
      recordsListEl.innerHTML = '<div class="settings-help">No vault entries saved yet.</div>';
      return;
    }

    recordsListEl.innerHTML = records.map((record) => `
      <button
        type="button"
        class="btn btn-outline-secondary text-start"
        data-record-id="${escapeHTML(record.id)}"
        style="border-radius: 12px; padding: 0.9rem 1rem; background: ${selectedRecord?.id === record.id ? 'var(--bg-tertiary)' : 'transparent'};"
      >
        <div class="d-flex justify-content-between align-items-start gap-2">
          <div>
            <div style="color: var(--text-primary); font-weight: 600;">${escapeHTML(record.label || record.type || 'Untitled')}</div>
            <div style="color: var(--text-secondary); font-size: 0.85rem;">${escapeHTML(record.type)}${record.workspace_id ? ` • ${escapeHTML(record.workspace_id)}` : ''}</div>
          </div>
          <span class="badge" style="background: var(--bg-tertiary); color: var(--text-primary);">${(record.tags || []).length} tags</span>
        </div>
      </button>
    `).join('');
  }

  function renderGrantsList(items) {
    grants = Array.isArray(items) ? items : [];

    if (!grants.length) {
      grantsListEl.innerHTML = '<div class="settings-help">No persistent grants configured.</div>';
      return;
    }

    grantsListEl.innerHTML = grants.map((grant) => `
      <div class="settings-info-box">
        <div class="settings-info-row" style="align-items: flex-start;">
          <div>
            <div style="color: var(--text-primary); font-weight: 600;">${escapeHTML(grant.actor_type)}:${escapeHTML(grant.actor_id)}</div>
            <div style="color: var(--text-secondary); font-size: 0.85rem;">${escapeHTML(grant.workspace_id)} • ${escapeHTML(grant.capability)} • ${escapeHTML(grant.record_type || '*')}</div>
          </div>
          <button type="button" class="btn btn-sm btn-outline-danger" data-grant-id="${escapeHTML(grant.id)}">Revoke</button>
        </div>
      </div>
    `).join('');
  }

  async function loadVaultStatus() {
    if (!currentVaultID()) {
      const status = {
        available: false,
        locked: true,
        writable: false,
        password_protected: false,
        requires_passphrase: false,
        message: 'Create a vault to begin storing encrypted records.',
        record_count: 0,
        secret_store: {
          backend: 'unavailable',
          available: false,
          writable: false,
          locked: true
        }
      };
      renderStatus(status);
      return status;
    }

    const status = await apiRequest(vaultURL('/api/vault/status'));
    renderStatus(status);
    return status;
  }

  async function loadVaultRecords() {
    if (!vaultStatus?.available || vaultStatus?.locked) {
      renderRecordsList([]);
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/records'));
    renderRecordsList(data.records || []);
  }

  async function loadVaultGrants() {
    if (!currentVaultID()) {
      renderGrantsList([]);
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/grants'));
    renderGrantsList(data.grants || []);
  }

  async function refreshVault() {
    try {
      await loadVaults();
      const status = await loadVaultStatus();
      if (!status.available) {
        renderVaultSpaces();
        renderGrantsList([]);
        renderRecordsList([]);
        return;
      }
      await loadVaultGrants();
      if (!status.locked) {
        await loadVaultRecords();
      } else {
        renderVaultSpaces();
      }
    } catch (error) {
      console.error('Failed to refresh vault:', error);
      showInlineAlert(error.message || 'Failed to refresh vault.', 'error');
    }
  }

  async function selectRecord(recordID) {
    try {
      const record = await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(recordID)}`));
      applyRecordToForm(record);
      renderRecordsList(records);
    } catch (error) {
      console.error('Failed to load vault record:', error);
      showInlineAlert(error.message || 'Failed to load vault record.', 'error');
    }
  }

  async function saveRecord() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before saving an entry.', 'warning');
      return;
    }

    const payloadBody = {
      vault_id: currentVaultID(),
      type: entryTypeInput.value,
      workspace_id: entryWorkspaceInput.value.trim(),
      label: entryLabelInput.value.trim(),
      tags: parseTags(entryTagsInput.value),
      retention_policy: entryRetentionInput.value.trim()
    };

    if (!payloadBody.label) {
      showInlineAlert('Label is required before saving to the vault.', 'warning');
      return;
    }

    try {
      if (!selectedRecord || payloadRevealed) {
        payloadBody.payload = parsePayloadInput();
      }
    } catch (error) {
      showInlineAlert(error.message || 'Payload must be valid JSON.', 'warning');
      return;
    }

    try {
      setButtonLoading(saveEntryBtn, true, selectedRecord ? 'Saving changes' : 'Saving entry');

      let response;
      if (selectedRecord) {
        response = await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(selectedRecord.id)}`), {
          method: 'PATCH',
          body: payloadBody
        });
      } else {
        response = await apiRequest('/api/vault/records', {
          method: 'POST',
          body: payloadBody
        });
      }

      notify(selectedRecord ? 'Vault entry updated.' : 'Vault entry saved.', 'success');
      await refreshVault();

      const nextRecordID = response?.record?.id;
      if (nextRecordID) {
        await selectRecord(nextRecordID);
      } else {
        clearRecordForm();
      }
    } catch (error) {
      console.error('Failed to save vault entry:', error);
      showInlineAlert(error.message || 'Failed to save vault entry.', 'error');
    } finally {
      setButtonLoading(saveEntryBtn, false);
    }
  }

  async function deleteRecord() {
    if (!selectedRecord) {
      return;
    }

    const confirmed = window.confirm(`Delete vault entry "${selectedRecord.label || selectedRecord.id}"?`);
    if (!confirmed) {
      return;
    }

    try {
      setButtonLoading(deleteEntryBtn, true, 'Deleting');
      await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(selectedRecord.id)}`), {
        method: 'DELETE'
      });
      notify('Vault entry deleted.', 'success');
      clearRecordForm();
      await refreshVault();
    } catch (error) {
      console.error('Failed to delete vault entry:', error);
      showInlineAlert(error.message || 'Failed to delete vault entry.', 'error');
    } finally {
      setButtonLoading(deleteEntryBtn, false);
    }
  }

  async function unlockVault() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before unlocking storage access.', 'warning');
      return;
    }

    try {
      setButtonLoading(unlockBtn, true, 'Unlocking');
      await apiRequest(vaultURL('/api/vault/unlock'), {
        method: 'POST',
        body: {
          vault_id: currentVaultID(),
          vault_password: unlockPasswordInput.value
        }
      });
      unlockPasswordInput.value = '';
      notify('Vault unlocked.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to unlock vault:', error);
      showInlineAlert(error.message || 'Failed to unlock vault.', 'error');
    } finally {
      setButtonLoading(unlockBtn, false);
    }
  }

  async function lockVault() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before changing vault access.', 'warning');
      return;
    }

    try {
      setButtonLoading(lockBtn, true, 'Locking');
      await apiRequest(vaultURL('/api/vault/lock'), {
        method: 'POST',
        body: {}
      });
      notify('Vault locked.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to lock vault:', error);
      showInlineAlert(error.message || 'Failed to lock vault.', 'error');
    } finally {
      setButtonLoading(lockBtn, false);
    }
  }

  async function createGrant() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before configuring grants.', 'warning');
      return;
    }

    const workspaceID = grantWorkspaceInput.value.trim();
    const actorID = grantActorIDInput.value.trim();

    if (!workspaceID || !actorID) {
      showInlineAlert('Workspace ID and actor ID are required for persistent grants.', 'warning');
      return;
    }

    try {
      setButtonLoading(createGrantBtn, true, 'Saving grant');
      await apiRequest('/api/vault/grants', {
        method: 'POST',
        body: {
          vault_id: currentVaultID(),
          workspace_id: workspaceID,
          actor_type: grantActorTypeInput.value,
          actor_id: actorID,
          capability: grantCapabilityInput.value,
          record_type: grantRecordTypeInput.value.trim()
        }
      });
      notify('Persistent grant saved.', 'success');
      await loadVaultGrants();
    } catch (error) {
      console.error('Failed to save grant:', error);
      showInlineAlert(error.message || 'Failed to save grant.', 'error');
    } finally {
      setButtonLoading(createGrantBtn, false);
    }
  }

  async function revokeGrant(grantID) {
    const confirmed = window.confirm('Revoke this persistent vault grant?');
    if (!confirmed) {
      return;
    }

    try {
      await apiRequest(vaultURL(`/api/vault/grants/${encodeURIComponent(grantID)}`), {
        method: 'DELETE'
      });
      notify('Persistent grant revoked.', 'success');
      await loadVaultGrants();
    } catch (error) {
      console.error('Failed to revoke grant:', error);
      showInlineAlert(error.message || 'Failed to revoke grant.', 'error');
    }
  }

  async function exportVault() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before exporting.', 'warning');
      return;
    }

    if (!exportConfirmInput.checked) {
      showInlineAlert('Confirm the export warning before generating an export.', 'warning');
      return;
    }

    const exportPassword = String(exportPasswordInput?.value || '').trim();
    if (!exportPassword) {
      showInlineAlert('Enter an export password before exporting.', 'warning');
      return;
    }

    try {
      setButtonLoading(exportBtn, true, 'Exporting');
      const bundle = await apiRequest('/api/vault/export', {
        method: 'POST',
        body: {
          vault_id: currentVaultID(),
          workspace_id: exportWorkspaceInput.value.trim(),
          vault_password: exportPassword,
          confirm: true
        }
      });

      const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `ori-vault-export-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      notify('Vault export generated.', 'success');
    } catch (error) {
      console.error('Failed to export vault:', error);
      showInlineAlert(error.message || 'Failed to export vault.', 'error');
    } finally {
      setButtonLoading(exportBtn, false);
    }
  }

  async function importVault() {
    const file = importFileInput?.files?.[0] || null;
    if (!file) {
      showInlineAlert('Choose an export bundle before importing.', 'warning');
      return;
    }

    const importPassword = String(importPasswordInput?.value || '').trim();
    if (!importPassword) {
      showInlineAlert('Enter the import password before restoring a vault bundle.', 'warning');
      return;
    }

    const mode = importModeInput?.value === 'current' && canImportIntoCurrentVault() ? 'current' : 'new';
    if (mode === 'current' && !canImportIntoCurrentVault()) {
      showInlineAlert('Unlock the selected vault before importing into it.', 'warning');
      return;
    }

    let bundle = null;
    try {
      const raw = await readFileAsText(file);
      bundle = JSON.parse(raw);
    } catch (error) {
      console.error('Failed to parse vault import bundle:', error);
      showInlineAlert(error.message || 'The selected import file is not valid JSON.', 'error');
      return;
    }

    const requestBody = {
      import_password: importPassword,
      bundle,
      restore_grants: Boolean(importRestoreGrantsInput?.checked)
    };

    if (mode === 'current') {
      requestBody.target_vault_id = currentVaultID();
    } else {
      const vaultPassword = String(importVaultPasswordInput?.value || '').trim();
      const confirmPassword = String(importConfirmVaultPasswordInput?.value || '').trim();
      if (!vaultPassword) {
        showInlineAlert('Set a password for the imported vault before continuing.', 'warning');
        return;
      }
      if (vaultPassword !== confirmPassword) {
        showInlineAlert('The imported vault passwords do not match.', 'warning');
        return;
      }
      requestBody.create_vault = {
        name: String(importVaultNameInput?.value || '').trim(),
        description: String(importVaultDescriptionInput?.value || '').trim(),
        vault_password: vaultPassword
      };
    }

    try {
      setButtonLoading(importBtn, true, 'Importing');
      const response = await apiRequest('/api/vault/import', {
        method: 'POST',
        body: requestBody
      });

      const result = response?.result || {};
      if (result?.vault?.id) {
        selectedVaultID = result.vault.id;
        writeStoredVaultID(selectedVaultID);
      }

      if (importFileInput) importFileInput.value = '';
      if (importFileName) importFileName.textContent = 'No import file selected yet.';
      if (importPasswordInput) importPasswordInput.value = '';
      if (importVaultNameInput) importVaultNameInput.value = '';
      if (importVaultDescriptionInput) importVaultDescriptionInput.value = '';
      if (importVaultPasswordInput) importVaultPasswordInput.value = '';
      if (importConfirmVaultPasswordInput) importConfirmVaultPasswordInput.value = '';
      if (importRestoreGrantsInput) importRestoreGrantsInput.checked = true;
      if (importModeInput) importModeInput.value = 'new';

      notify(`Imported ${String(result.record_count || 0)} encrypted ${(result.record_count || 0) === 1 ? 'entry' : 'entries'} into ${vaultDisplayLabel(result.vault)}.`, 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to import vault bundle:', error);
      showInlineAlert(error.message || 'Failed to import vault bundle.', 'error');
    } finally {
      setButtonLoading(importBtn, false);
      syncImportControls();
    }
  }

  toggleUnlockPasswordBtn?.addEventListener('click', () => {
    if (!unlockPasswordInput) return;
    unlockPasswordInput.type = unlockPasswordInput.type === 'password' ? 'text' : 'password';
  });

  unlockBtn?.addEventListener('click', () => {
    unlockVault();
  });

  lockBtn?.addEventListener('click', () => {
    lockVault();
  });

  refreshBtn?.addEventListener('click', () => {
    refreshVault();
  });

  activeVaultSelect?.addEventListener('change', (event) => {
    switchVault(event.target.value);
  });

  renameVaultBtn?.addEventListener('click', () => {
    updateVaultSpace();
  });

  deleteVaultBtn?.addEventListener('click', () => {
    deleteVaultSpace();
  });

  createVaultSpaceBtn?.addEventListener('click', () => {
    createVaultSpace();
  });

  editVaultNameInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      updateVaultSpace();
    }
  });

  newVaultNameInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      createVaultSpace();
    }
  });

  confirmVaultPasswordInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      createVaultSpace();
    }
  });

  saveEntryBtn?.addEventListener('click', () => {
    saveRecord();
  });

  resetEntryBtn?.addEventListener('click', () => {
    clearRecordForm();
  });

  revealPayloadBtn?.addEventListener('click', () => {
    togglePayloadReveal();
  });

  deleteEntryBtn?.addEventListener('click', () => {
    deleteRecord();
  });

  recordsListEl?.addEventListener('click', (event) => {
    const trigger = event.target.closest('[data-record-id]');
    if (!trigger) {
      return;
    }
    selectRecord(trigger.getAttribute('data-record-id'));
  });

  createGrantBtn?.addEventListener('click', () => {
    createGrant();
  });

  grantsListEl?.addEventListener('click', (event) => {
    const trigger = event.target.closest('[data-grant-id]');
    if (!trigger) {
      return;
    }
    revokeGrant(trigger.getAttribute('data-grant-id'));
  });

  exportBtn?.addEventListener('click', () => {
    exportVault();
  });

  importChooseFileBtn?.addEventListener('click', () => {
    importFileInput?.click();
  });

  importFileInput?.addEventListener('change', () => {
    const file = importFileInput?.files?.[0] || null;
    if (importFileName) {
      importFileName.textContent = file ? String(file.name || 'import-bundle.json') : 'No import file selected yet.';
    }
  });

  importModeInput?.addEventListener('change', () => {
    syncImportControls();
  });

  importConfirmVaultPasswordInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      importVault();
    }
  });

  importPasswordInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      importVault();
    }
  });

  importBtn?.addEventListener('click', () => {
    importVault();
  });

  clearRecordForm();
  syncImportControls();
  refreshVault();
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

// Notes Open Behavior preference (server-persisted via /api/notes-open-behavior,
// mirrored to localStorage so the page-load routing path stays synchronous).
(function() {
  const STORAGE_KEY = 'note.openBehavior';
  const VALID = new Set(['modal', 'page', 'page-new-tab']);
  const radios = Array.from(document.querySelectorAll('input[name="notesOpenBehavior"]'));
  const statusEl = document.getElementById('notesOpenBehaviorStatus');
  if (!radios.length) return;

  function refreshOptionState() {
    document.querySelectorAll('label.ui-density-option').forEach((el) => {
      const input = el.querySelector('input[name="notesOpenBehavior"]');
      if (!input) return;
      el.classList.toggle('is-selected', Boolean(input.checked));
    });
  }

  function setLocal(value) {
    try { localStorage.setItem(STORAGE_KEY, value); } catch (_) {}
  }

  function readLocal() {
    try {
      const v = localStorage.getItem(STORAGE_KEY);
      return VALID.has(v) ? v : null;
    } catch (_) { return null; }
  }

  function select(value, showNotice = false) {
    const v = VALID.has(value) ? value : 'modal';
    radios.forEach((r) => { r.checked = r.value === v; });
    refreshOptionState();
    setLocal(v);
    if (showNotice && statusEl) {
      statusEl.textContent = 'Saved.';
      setTimeout(() => { if (statusEl.textContent === 'Saved.') statusEl.textContent = ''; }, 1500);
    }
  }

  async function persist(value) {
    try {
      const resp = await fetch('/api/notes-open-behavior', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ behavior: value }),
      });
      if (!resp.ok) {
        console.warn('Failed to persist notes_open_behavior:', resp.status);
        if (statusEl) statusEl.textContent = 'Could not save preference.';
      }
    } catch (err) {
      console.warn('notes_open_behavior save errored', err);
      if (statusEl) statusEl.textContent = 'Could not save preference.';
    }
  }

  radios.forEach((input) => {
    input.addEventListener('change', () => {
      if (!input.checked) return;
      select(input.value, true);
      persist(input.value);
    });
  });

  // Initial paint: local mirror first (instant), then reconcile with the server.
  const local = readLocal();
  if (local) select(local, false);
  fetch('/api/notes-open-behavior').then((r) => r.ok ? r.json() : null).then((data) => {
    const v = data?.behavior;
    if (VALID.has(v)) select(v, false);
  }).catch(() => {});
})();

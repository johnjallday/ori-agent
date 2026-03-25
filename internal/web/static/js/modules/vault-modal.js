(function() {
  const ROOT_FOLDER_PATH = 'vault';
  const TYPE_FOLDER_PATH = 'types';
  const WORKSPACE_FOLDER_PATH = 'workspaces';
  const DEFAULT_VAULT_ID = '';
  const VAULT_STORAGE_KEY = 'ori-selected-vault-id';
  const DEFAULT_EXPANDED_FOLDERS = new Set([ROOT_FOLDER_PATH, TYPE_FOLDER_PATH, WORKSPACE_FOLDER_PATH]);

  const TYPE_META = {
    personal_note: {
      label: 'Personal Note',
      folderName: 'Personal Notes'
    },
    email_snippet: {
      label: 'Email Snippet',
      folderName: 'Email Snippets'
    },
    secret: {
      label: 'Secret',
      folderName: 'Secrets'
    }
  };

  const state = {
    vaults: [],
    selectedVaultID: DEFAULT_VAULT_ID,
    status: null,
    records: [],
    recordIndex: new Map(),
    selectedRecord: null,
    payloadRevealed: false,
    folderIndex: new Map(),
    expandedFolderPaths: new Set(DEFAULT_EXPANDED_FOLDERS),
    selectedFolderPath: ROOT_FOLDER_PATH,
    hasHydrated: false,
    isHydrating: false,
    unlockDialogOpen: false
  };

  let alertTimeoutId = 0;
  let elements = null;

  function escapeHTML(value) {
    return String(value || '')
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  function notify(message, type) {
    if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, type || 'info');
      return;
    }

    if (window.Toast && typeof window.Toast.show === 'function') {
      window.Toast.show(message, type || 'info');
      return;
    }

    console[type === 'error' ? 'error' : 'log'](message);
  }

  function getElements() {
    const modal = document.getElementById('vaultModal');
    const launcher = document.getElementById('vaultLauncherButton');

    if (!modal || !launcher) {
      return null;
    }

    return {
      modal,
      launcher,
      launcherCount: document.getElementById('vaultNavCount'),
      title: document.getElementById('vaultModalLabel'),
      subtitle: document.getElementById('vaultModalSubtitle'),
      alert: document.getElementById('vaultModalAlert'),
      loadingState: document.getElementById('vaultModalLoadingState'),
      statusShell: document.getElementById('vaultModalStatusShell'),
      statusIndicator: document.getElementById('vaultModalStatusIndicator'),
      statusText: document.getElementById('vaultModalStatusText'),
      statusDetails: document.getElementById('vaultModalStatusDetails'),
      backendLabel: document.getElementById('vaultModalBackendLabel'),
      recordCountLabel: document.getElementById('vaultModalRecordCount'),
      writableLabel: document.getElementById('vaultModalWritableLabel'),
      modeLabel: document.getElementById('vaultModalModeLabel'),
      vaultShell: document.getElementById('vaultModalVaultShell'),
      vaultSelectionStack: document.getElementById('vaultModalVaultSelectionStack'),
      selectedVaultState: document.getElementById('vaultModalSelectedVaultState'),
      selectedVaultUnlockBtn: document.getElementById('vaultModalSelectedVaultUnlockBtn'),
      lockBtn: document.getElementById('vaultModalLockBtn'),
      refreshBtn: document.getElementById('vaultModalRefreshBtn'),
      vaultManageStack: document.getElementById('vaultModalVaultManageStack'),
      vaultCreateStack: document.getElementById('vaultModalVaultCreateStack'),
      vaultCreateTitle: document.getElementById('vaultModalVaultCreateTitle'),
      vaultSelect: document.getElementById('vaultModalVaultSelect'),
      vaultMeta: document.getElementById('vaultModalVaultMeta'),
      vaultRail: document.getElementById('vaultModalVaultRail'),
      editVaultNameInput: document.getElementById('vaultModalEditVaultName'),
      editVaultDescriptionInput: document.getElementById('vaultModalEditVaultDescription'),
      renameVaultBtn: document.getElementById('vaultModalRenameVaultBtn'),
      deleteVaultBtn: document.getElementById('vaultModalDeleteVaultBtn'),
      newVaultNameInput: document.getElementById('vaultModalNewVaultName'),
      newVaultDescriptionInput: document.getElementById('vaultModalNewVaultDescription'),
      newVaultPasswordInput: document.getElementById('vaultModalNewVaultPassword'),
      confirmVaultPasswordInput: document.getElementById('vaultModalConfirmVaultPassword'),
      createVaultBtn: document.getElementById('vaultModalCreateVaultBtn'),
      unlockOverlay: document.getElementById('vaultModalUnlockOverlay'),
      unlockDialogTitle: document.getElementById('vaultModalUnlockDialogTitle'),
      unlockDialogDescription: document.getElementById('vaultModalUnlockDialogDescription'),
      unlockVaultName: document.getElementById('vaultModalUnlockVaultName'),
      passwordInput: document.getElementById('vaultModalUnlockPassword'),
      passwordHelp: document.getElementById('vaultModalUnlockPasswordHelp'),
      togglePasswordBtn: document.getElementById('vaultModalUnlockTogglePassword'),
      unlockBtn: document.getElementById('vaultModalUnlockBtn'),
      unlockCancelBtn: document.getElementById('vaultModalUnlockCancelBtn'),
      entryTypeInput: document.getElementById('vaultModalEntryType'),
      entryWorkspaceInput: document.getElementById('vaultModalEntryWorkspaceId'),
      entryLabelInput: document.getElementById('vaultModalEntryLabel'),
      entryTagsInput: document.getElementById('vaultModalEntryTags'),
      entrySourceInput: document.getElementById('vaultModalEntrySource'),
      entryRetentionInput: document.getElementById('vaultModalEntryRetention'),
      entryPayloadInput: document.getElementById('vaultModalEntryPayload'),
      saveBtn: document.getElementById('vaultModalSaveBtn'),
      resetBtn: document.getElementById('vaultModalResetBtn'),
      mainGrid: document.getElementById('vaultModalMainGrid'),
      searchInput: document.getElementById('vaultModalSearchInput'),
      recordsSummary: document.getElementById('vaultModalRecordsSummary'),
      breadcrumb: document.getElementById('vaultModalFolderBreadcrumb'),
      folderTree: document.getElementById('vaultModalFolderTree'),
      preview: document.getElementById('vaultModalExplorerPreview'),
      settingsLink: document.getElementById('vaultModalSettingsLink'),
      footer: modal.querySelector('.vault-modal-footer')
    };
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

  function activeVaultID() {
    return String(state.selectedVaultID || '').trim();
  }

  function activeVault() {
    return state.vaults.find(function(item) {
      return item.id === activeVaultID();
    }) || null;
  }

  function vaultURL(path, vaultID) {
    const selectedVaultID = String(vaultID || activeVaultID()).trim();
    if (!selectedVaultID) {
      return path;
    }

    const separator = path.includes('?') ? '&' : '?';
    return path + separator + 'vault_id=' + encodeURIComponent(selectedVaultID);
  }

  async function apiRequest(url, options) {
    const request = {
      method: options?.method || 'GET',
      headers: {
        'Content-Type': 'application/json',
        ...(options?.headers || {})
      }
    };

    if (options && Object.prototype.hasOwnProperty.call(options, 'body') && request.method !== 'GET') {
      request.body = JSON.stringify(options.body);
    }

    const response = await fetch(url, request);
    const contentType = response.headers.get('content-type') || '';
    let data = null;

    if (contentType.includes('application/json')) {
      data = await response.json();
    } else {
      const text = await response.text();
      data = text ? { error: text } : {};
    }

    if (!response.ok) {
      throw new Error(data?.error || data?.message || response.statusText || 'Request failed');
    }

    return data;
  }

  function setButtonLoading(button, loading, loadingLabel) {
    if (!button) {
      return;
    }

    if (loading) {
      button.dataset.originalLabel = button.innerHTML;
      button.disabled = true;
      button.innerHTML = '<span class="spinner-border spinner-border-sm me-2" aria-hidden="true"></span>' + String(loadingLabel || 'Working');
      return;
    }

    if (button.dataset.originalLabel) {
      button.innerHTML = button.dataset.originalLabel;
      delete button.dataset.originalLabel;
    }
    button.disabled = false;
  }

  function showAlert(message, type) {
    if (!elements || !elements.alert) {
      notify(message, type);
      return;
    }

    if (!message) {
      elements.alert.innerHTML = '';
      return;
    }

    const classMap = {
      success: 'alert-success',
      error: 'alert-danger',
      warning: 'alert-warning',
      info: 'alert-info'
    };

    elements.alert.innerHTML = (
      '<div class="alert ' + (classMap[type] || classMap.info) + ' mb-0" role="alert">' +
        escapeHTML(message) +
      '</div>'
    );

    window.clearTimeout(alertTimeoutId);
    alertTimeoutId = window.setTimeout(function() {
      if (elements && elements.alert) {
        elements.alert.innerHTML = '';
      }
    }, 4500);
  }

  function parseTags(rawValue) {
    return String(rawValue || '')
      .split(',')
      .map(function(tag) {
        return tag.trim();
      })
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
    const raw = String(elements?.entryPayloadInput?.value || '').trim();
    if (!raw) {
      return {};
    }

    try {
      return JSON.parse(raw);
    } catch (error) {
      throw new Error('Payload must be valid JSON.');
    }
  }

  function prettyDate(value) {
    if (!value) {
      return 'Not available';
    }

    try {
      return new Date(value).toLocaleString();
    } catch (error) {
      return String(value);
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

  function defaultPayloadValue() {
    return '{\n  \n}';
  }

  function normalizeRecordType(type) {
    return String(type || '').trim().toLowerCase() || 'personal_note';
  }

  function recordTypeLabel(type) {
    const normalized = normalizeRecordType(type);
    return TYPE_META[normalized]?.label || normalized.replaceAll('_', ' ');
  }

  function recordTypeFolderName(type) {
    const normalized = normalizeRecordType(type);
    return TYPE_META[normalized]?.folderName || recordTypeLabel(normalized);
  }

  function folderPathForType(type) {
    return 'type::' + normalizeRecordType(type);
  }

  function folderPathForWorkspace(workspaceID) {
    const normalized = String(workspaceID || '').trim();
    return normalized ? 'workspace::' + normalized : 'workspace::global';
  }

  function fileTypeIcon(type) {
    const normalized = normalizeRecordType(type);

    if (normalized === 'secret') {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12,1A5,5 0 0,1 17,6V8H18A2,2 0 0,1 20,10V21A2,2 0 0,1 18,23H6A2,2 0 0,1 4,21V10A2,2 0 0,1 6,8H7V6A5,5 0 0,1 12,1M12,3A3,3 0 0,0 9,6V8H15V6A3,3 0 0,0 12,3M12,12A2,2 0 0,0 10,14A2,2 0 0,0 11,15.73V18H13V15.73A2,2 0 0,0 14,14A2,2 0 0,0 12,12Z"/></svg>';
    }

    if (normalized === 'email_snippet') {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M4,4H20A2,2 0 0,1 22,6V18A2,2 0 0,1 20,20H4A2,2 0 0,1 2,18V6A2,2 0 0,1 4,4M4,6V8L12,13L20,8V6L12,11L4,6M4,18H20V10.5L12,15.5L4,10.5V18Z"/></svg>';
    }

    return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';
  }

  function folderIcon() {
    return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M10,4H4A2,2 0 0,0 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8A2,2 0 0,0 20,6H12L10,4Z"/></svg>';
  }

  function setLauncherActive(active) {
    if (!elements || !elements.launcher) {
      return;
    }

    elements.launcher.classList.toggle('active', Boolean(active));
    elements.launcher.setAttribute('aria-expanded', active ? 'true' : 'false');
  }

  function updateLauncherCount(status) {
    if (!elements || !elements.launcherCount) {
      return;
    }

    if (!state.hasHydrated || !status?.available) {
      elements.launcherCount.textContent = '0';
      elements.launcherCount.classList.add('is-hidden');
      return;
    }

    elements.launcherCount.textContent = String(status?.record_count || 0);
    elements.launcherCount.classList.remove('is-hidden');
  }

  function vaultRecordCount(vaultItem) {
    const count = Number(vaultItem?.record_count || 0);
    return Number.isFinite(count) ? count : 0;
  }

  function vaultDisplayLabel(vaultItem) {
    if (!vaultItem) {
      return 'Vault';
    }
    return String(vaultItem.name || 'Vault');
  }

  function syncUnlockDialog() {
    if (!elements?.unlockOverlay) {
      return;
    }

    const selectedVault = activeVault();
    const vaultLabel = vaultDisplayLabel(selectedVault);
    const canShow = Boolean(state.unlockDialogOpen && state.status && state.status.available && state.status.locked);

    if (elements.unlockVaultName) {
      elements.unlockVaultName.textContent = vaultLabel;
    }

    if (elements.unlockDialogDescription) {
      elements.unlockDialogDescription.textContent = 'Enter the password for ' + vaultLabel + ' to load it inside Ori.';
    }

    elements.unlockOverlay.hidden = !canShow;
    elements.modal?.classList.toggle('has-unlock-dialog', canShow);

    if (!canShow && elements.passwordInput) {
      elements.passwordInput.value = '';
      elements.passwordInput.type = 'password';
    }
  }

  function applyModalMode() {
    if (!elements) {
      return;
    }

    const isInitialHydrate = state.isHydrating && !state.hasHydrated;
    const hasVaults = state.hasHydrated && state.vaults.length > 0;
    const showEmptyCreateMode = state.hasHydrated && state.vaults.length === 0;
    const unlocked = Boolean(state.status && state.status.available && !state.status.locked);
    const settingsHidden = isInitialHydrate || showEmptyCreateMode || !unlocked;

    if (elements.title) {
      elements.title.textContent = showEmptyCreateMode ? 'Create Your First Vault' : 'Private Vault';
    }

    if (elements.subtitle) {
      if (isInitialHydrate) {
        elements.subtitle.textContent = 'Vault data is fetched on demand. Ori is loading your vault list and encrypted status now.';
      } else if (showEmptyCreateMode) {
        elements.subtitle.textContent = 'Start with a single encrypted vault. Once it exists, unlock it to reveal entry tools and the protected folder view.';
      } else if (hasVaults && !unlocked) {
        elements.subtitle.textContent = 'Choose or create a vault, then unlock it. Entry creation and folder browsing stay hidden until the selected vault is actually loaded.';
      } else {
        elements.subtitle.textContent = 'Vault creation, encrypted entry capture, and folder browsing stay grouped here, but only loaded after you unlock the active vault.';
      }
    }

    if (elements.loadingState) {
      elements.loadingState.hidden = !isInitialHydrate;
    }

    if (elements.statusShell) {
      elements.statusShell.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
    }

    if (elements.vaultSelectionStack) {
      elements.vaultSelectionStack.hidden = isInitialHydrate || showEmptyCreateMode;
    }

    if (elements.selectedVaultState) {
      elements.selectedVaultState.hidden = isInitialHydrate || showEmptyCreateMode || !hasVaults;
    }

    if (elements.selectedVaultUnlockBtn) {
      elements.selectedVaultUnlockBtn.hidden = isInitialHydrate || showEmptyCreateMode || !hasVaults || unlocked;
    }

    if (elements.vaultManageStack) {
      elements.vaultManageStack.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
    }

    if (elements.vaultCreateTitle) {
      elements.vaultCreateTitle.textContent = showEmptyCreateMode ? 'Create A Vault' : 'New Vault';
    }

    if (elements.createVaultBtn && !elements.createVaultBtn.disabled) {
      elements.createVaultBtn.textContent = showEmptyCreateMode ? 'Create First Vault' : 'Create Vault';
    }

    if (elements.vaultCreateStack) {
      elements.vaultCreateStack.classList.toggle('is-primary', showEmptyCreateMode);
    }

    if (elements.vaultShell) {
      elements.vaultShell.hidden = isInitialHydrate;
      elements.vaultShell.classList.toggle('is-empty', showEmptyCreateMode);
      elements.vaultShell.classList.toggle('is-compact', hasVaults && !unlocked);
    }

    if (elements.lockBtn) {
      elements.lockBtn.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
    }

    if (elements.refreshBtn) {
      elements.refreshBtn.hidden = isInitialHydrate || showEmptyCreateMode || !hasVaults;
    }

    if (elements.mainGrid) {
      elements.mainGrid.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
    }

    if (elements.settingsLink) {
      elements.settingsLink.hidden = settingsHidden;
    }

    if (elements.footer) {
      elements.footer.classList.toggle('is-empty', settingsHidden);
    }

    syncUnlockDialog();
  }

  function syncVaultEditor() {
    if (!elements) {
      return;
    }

    const selectedVault = activeVault();
    const canEdit = Boolean(selectedVault);

    if (elements.editVaultNameInput) {
      elements.editVaultNameInput.value = selectedVault?.name || '';
      elements.editVaultNameInput.disabled = !canEdit;
    }

    if (elements.editVaultDescriptionInput) {
      elements.editVaultDescriptionInput.value = selectedVault?.description || '';
      elements.editVaultDescriptionInput.disabled = !canEdit;
    }

    if (elements.renameVaultBtn) {
      elements.renameVaultBtn.disabled = !canEdit;
    }

    if (elements.deleteVaultBtn) {
      elements.deleteVaultBtn.disabled = !canEdit;
      elements.deleteVaultBtn.title = '';
    }
  }

  function renderVaultRail() {
    if (!elements?.vaultRail) {
      return;
    }

    if (!state.vaults.length) {
      elements.vaultRail.innerHTML = '';
      return;
    }

    elements.vaultRail.innerHTML = state.vaults.map(function(item) {
      const activeClass = item.id === activeVaultID() ? ' is-active' : '';
      return (
        '<button type="button" class="vault-modal-vault-pill' + activeClass + '" data-action="select-vault-chip" data-vault-id="' + escapeHTML(item.id) + '">' +
          '<span class="vault-modal-vault-pill-label">' + escapeHTML(item.name) + '</span>' +
          '<span class="vault-modal-vault-pill-count">' + String(vaultRecordCount(item)) + '</span>' +
        '</button>'
      );
    }).join('');
  }

  function renderVaultSelector() {
    if (!elements?.vaultSelect || !elements?.vaultMeta) {
      return;
    }

    if (!state.vaults.length) {
      elements.vaultSelect.innerHTML = '<option value="">No vaults available</option>';
      elements.vaultMeta.textContent = 'Create a vault to begin storing encrypted records.';
      if (elements.selectedVaultState) {
        elements.selectedVaultState.textContent = 'No vault loaded yet.';
      }
      renderVaultRail();
      syncVaultEditor();
      return;
    }

    elements.vaultSelect.innerHTML = state.vaults.map(function(item) {
      const selected = item.id === activeVaultID() ? ' selected' : '';
      const label = vaultDisplayLabel(item) + ' · ' + String(vaultRecordCount(item));
      return '<option value="' + escapeHTML(item.id) + '"' + selected + '>' + escapeHTML(label) + '</option>';
    }).join('');

    const selectedVault = activeVault();
    if (!selectedVault) {
      elements.vaultMeta.textContent = 'Select a vault to continue.';
      if (elements.selectedVaultState) {
        elements.selectedVaultState.textContent = 'Select a vault to continue.';
      }
      renderVaultRail();
      syncVaultEditor();
      return;
    }

    const metaParts = [];
    if (selectedVault.description) {
      metaParts.push(selectedVault.description);
    }
    metaParts.push(selectedVault.password_protected ? 'Own password' : 'Legacy vault');
    metaParts.push(String(vaultRecordCount(selectedVault)) + ' stored ' + (vaultRecordCount(selectedVault) === 1 ? 'entry' : 'entries'));
    elements.vaultMeta.textContent = metaParts.join(' • ');
    renderVaultRail();
    syncVaultEditor();
  }

  function closeUnlockDialog(options) {
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    const restoreFocus = options?.restoreFocus !== false;
    if (restoreFocus && elements?.modal?.classList.contains('show') && state.status?.locked) {
      window.requestAnimationFrame(function() {
        elements?.selectedVaultUnlockBtn?.focus();
      });
    }
  }

  function openUnlockDialog() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before unlocking storage access.', 'warning');
      return;
    }

    if (!state.status || !state.status.available) {
      showAlert('Select a vault before unlocking storage access.', 'warning');
      return;
    }

    if (!state.status.locked) {
      return;
    }

    state.unlockDialogOpen = true;
    syncUnlockDialog();
    window.requestAnimationFrame(function() {
      elements?.passwordInput?.focus();
    });
  }

  async function loadVaults(preserveSelection) {
    const data = await apiRequest('/api/vault/vaults');
    state.vaults = Array.isArray(data.vaults) ? data.vaults : [];

    const preferredVaultID = String(state.selectedVaultID || readStoredVaultID() || '').trim();

    const nextVault = state.vaults.find(function(item) {
      return item.id === preferredVaultID;
    }) || state.vaults[0] || null;

    state.selectedVaultID = nextVault?.id || DEFAULT_VAULT_ID;
    writeStoredVaultID(state.selectedVaultID);
    applyModalMode();
    renderVaultSelector();
  }

  async function switchVault(nextVaultID, options) {
    nextVaultID = String(nextVaultID || '').trim();
    const promptUnlock = Boolean(options?.promptUnlock);

    if (!nextVaultID || nextVaultID === activeVaultID()) {
      renderVaultSelector();
      if (promptUnlock && state.status?.locked) {
        openUnlockDialog();
      }
      return;
    }

    state.selectedVaultID = nextVaultID;
    writeStoredVaultID(state.selectedVaultID);
    clearSelection();
    state.selectedFolderPath = ROOT_FOLDER_PATH;
    await refreshVault(false);

    if (promptUnlock && state.status?.locked) {
      openUnlockDialog();
      return;
    }

    closeUnlockDialog();
  }

  async function createVault() {
    const name = String(elements?.newVaultNameInput?.value || '').trim();
    const description = String(elements?.newVaultDescriptionInput?.value || '').trim();
    const password = String(elements?.newVaultPasswordInput?.value || '');
    const confirmPassword = String(elements?.confirmVaultPasswordInput?.value || '');

    if (!name) {
      showAlert('New vault name is required before creating a vault.', 'warning');
      return;
    }
    if (!password.trim()) {
      showAlert('A vault password is required before creating a vault.', 'warning');
      return;
    }
    if (password !== confirmPassword) {
      showAlert('Vault password confirmation does not match.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.createVaultBtn, true, 'Creating vault');
      const response = await apiRequest('/api/vault/vaults', {
        method: 'POST',
        body: {
          name: name,
          description: description,
          vault_password: password
        }
      });

      if (elements.newVaultNameInput) {
        elements.newVaultNameInput.value = '';
      }
      if (elements.newVaultDescriptionInput) {
        elements.newVaultDescriptionInput.value = '';
      }
      if (elements.newVaultPasswordInput) {
        elements.newVaultPasswordInput.value = '';
      }
      if (elements.confirmVaultPasswordInput) {
        elements.confirmVaultPasswordInput.value = '';
      }

      await loadVaults(false);
      if (response?.vault?.id) {
        state.selectedVaultID = response.vault.id;
        writeStoredVaultID(state.selectedVaultID);
        renderVaultSelector();
      }
      clearSelection();
      state.selectedFolderPath = ROOT_FOLDER_PATH;
      notify('Vault created.', 'success');
      await refreshVault(false);
    } catch (error) {
      console.error('Failed to create vault:', error);
      showAlert(error.message || 'Failed to create vault.', 'error');
    } finally {
      setButtonLoading(elements.createVaultBtn, false);
    }
  }

  async function updateVault() {
    const selectedVault = activeVault();
    if (!selectedVault) {
      showAlert('Select a vault before updating its details.', 'warning');
      return;
    }

    const name = String(elements?.editVaultNameInput?.value || '').trim();
    const description = String(elements?.editVaultDescriptionInput?.value || '').trim();

    if (!name) {
      showAlert('Vault name is required before saving changes.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.renameVaultBtn, true, 'Saving vault');
      await apiRequest('/api/vault/vaults/' + encodeURIComponent(selectedVault.id), {
        method: 'PATCH',
        body: {
          name: name,
          description: description
        }
      });
      notify('Vault details updated.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to update vault:', error);
      showAlert(error.message || 'Failed to update vault.', 'error');
    } finally {
      setButtonLoading(elements.renameVaultBtn, false);
    }
  }

  async function deleteVault() {
    const selectedVault = activeVault();
    if (!selectedVault) {
      showAlert('Select a vault before deleting it.', 'warning');
      return;
    }
    const recordCount = vaultRecordCount(selectedVault);
    const confirmed = window.confirm(
      'Delete vault "' + selectedVault.name + '"?' +
      (recordCount > 0 ? ' This will permanently remove ' + String(recordCount) + ' encrypted ' + (recordCount === 1 ? 'entry' : 'entries') + '.' : '')
    );
    if (!confirmed) {
      return;
    }

    try {
      setButtonLoading(elements.deleteVaultBtn, true, 'Deleting vault');
      await apiRequest('/api/vault/vaults/' + encodeURIComponent(selectedVault.id), {
        method: 'DELETE'
      });
      state.selectedVaultID = DEFAULT_VAULT_ID;
      writeStoredVaultID(state.selectedVaultID);
      clearSelection();
      state.selectedFolderPath = ROOT_FOLDER_PATH;
      notify('Vault deleted.', 'success');
      await refreshVault(false);
    } catch (error) {
      console.error('Failed to delete vault:', error);
      showAlert(error.message || 'Failed to delete vault.', 'error');
    } finally {
      setButtonLoading(elements.deleteVaultBtn, false);
    }
  }

  function setInteractiveState(locked) {
    const createDisabled = !state.status || !state.status.available || Boolean(locked);
    const createFields = [
      elements.entryTypeInput,
      elements.entryWorkspaceInput,
      elements.entryLabelInput,
      elements.entryTagsInput,
      elements.entrySourceInput,
      elements.entryRetentionInput,
      elements.entryPayloadInput
    ];

    createFields.forEach(function(field) {
      if (field) {
        field.disabled = createDisabled;
      }
    });

    if (elements.saveBtn) {
      elements.saveBtn.disabled = createDisabled;
    }

    if (elements.resetBtn) {
      elements.resetBtn.disabled = false;
    }

    if (elements.lockBtn) {
      elements.lockBtn.disabled = !state.status || !state.status.available || Boolean(locked);
    }

    if (elements.refreshBtn) {
      elements.refreshBtn.disabled = !state.status || !state.status.available;
    }

    if (elements.selectedVaultUnlockBtn) {
      elements.selectedVaultUnlockBtn.disabled = !state.status || !state.status.available || !Boolean(locked);
    }

    if (elements.unlockBtn) {
      elements.unlockBtn.disabled = !state.status || !state.status.available || !Boolean(locked);
    }
  }

  function renderStatusIndicator(color) {
    if (!elements || !elements.statusIndicator) {
      return;
    }

    elements.statusIndicator.innerHTML = '<span class="vault-modal-status-dot" style="background:' + color + ';"></span>';
  }

  function renderStatus(status) {
    state.status = status || null;
    if (!elements || !state.status) {
      return;
    }

    if (!state.status.available) {
      renderStatusIndicator('#94a3b8');
      elements.statusText.textContent = 'No vault selected';
      elements.statusDetails.textContent = state.status.message || 'Create a vault to begin storing encrypted records.';
      elements.backendLabel.textContent = backendLabelFor(state.status);
      elements.recordCountLabel.textContent = '0';
      elements.writableLabel.textContent = 'Unavailable';
      elements.modeLabel.textContent = 'Create a vault';
      if (elements.passwordHelp) {
        elements.passwordHelp.textContent = 'Per-vault passwords are required for new vaults. Legacy vaults may still unlock through secure system storage or the older fallback passphrase flow.';
      }
      if (elements.selectedVaultState) {
        elements.selectedVaultState.textContent = 'Create a vault to begin.';
      }
      closeUnlockDialog({ restoreFocus: false });
      applyModalMode();
      updateLauncherCount(state.status);
      setInteractiveState(true);
      return;
    }

    const locked = Boolean(state.status.locked);
    const requiresPassphrase = Boolean(state.status.requires_passphrase);
    const passwordProtected = Boolean(state.status.password_protected);
    const backend = String(state.status.secret_store?.backend || '');
    const selectedVault = activeVault();
    const vaultName = selectedVault?.name || state.status.vault_name || 'Vault';

    renderStatusIndicator(locked ? '#f59e0b' : '#16a34a');
    elements.statusText.textContent = vaultName + (locked ? ' locked' : ' available');

    const detailParts = [];
    if (selectedVault?.description) {
      detailParts.push(selectedVault.description);
    }
    if (state.status.message) {
      detailParts.push(state.status.message);
    }
    detailParts.push(String(state.status.record_count || 0) + ' stored ' + ((state.status.record_count || 0) === 1 ? 'entry' : 'entries'));
    elements.statusDetails.textContent = detailParts.join(' • ');

    elements.backendLabel.textContent = backendLabelFor(state.status);
    elements.recordCountLabel.textContent = String(state.status.record_count || 0);
    elements.writableLabel.textContent = state.status.writable ? 'Writable' : 'Read-only / locked';

    if (passwordProtected && locked && requiresPassphrase) {
      elements.modeLabel.textContent = 'Vault password required';
    } else if (passwordProtected) {
      elements.modeLabel.textContent = 'Per-vault password';
    } else if (locked && requiresPassphrase) {
      elements.modeLabel.textContent = 'Legacy passphrase required';
    } else if (backend === 'passphrase_fallback') {
      elements.modeLabel.textContent = 'Passphrase fallback';
    } else {
      elements.modeLabel.textContent = 'OS secure storage';
    }

    if (elements.passwordHelp) {
      elements.passwordHelp.textContent = passwordProtected
        ? 'Enter the password for the selected vault to unlock it.'
        : 'This legacy vault may still unlock through secure system storage or the older fallback passphrase flow.';
    }
    if (elements.selectedVaultState) {
      elements.selectedVaultState.textContent = locked
        ? vaultName + ' is locked. Unlock it to reveal the encrypted folder.'
        : vaultName + ' is loaded and ready for encrypted entry access.';
    }

    applyModalMode();
    updateLauncherCount(state.status);
    setInteractiveState(locked);

    if (!locked) {
      closeUnlockDialog({ restoreFocus: false });
    } else {
      syncUnlockDialog();
    }
  }

  function resetCreateForm() {
    if (!elements) {
      return;
    }

    elements.entryTypeInput.value = 'personal_note';
    elements.entryWorkspaceInput.value = '';
    elements.entryLabelInput.value = '';
    elements.entryTagsInput.value = '';
    elements.entrySourceInput.value = '';
    elements.entryRetentionInput.value = '';
    elements.entryPayloadInput.value = defaultPayloadValue();
  }

  function clearSelection() {
    state.selectedRecord = null;
    state.payloadRevealed = false;
  }

  function renderSelection(record) {
    state.selectedRecord = record || null;
    state.payloadRevealed = false;
    renderExplorerPreview();
  }

  function togglePayloadReveal() {
    if (!state.selectedRecord) {
      return;
    }

    state.payloadRevealed = !state.payloadRevealed;
    renderExplorerPreview();
  }

  function createFolderNode(path, name, parentPath, recordIDs, options) {
    return {
      path: path,
      name: name,
      parentPath: parentPath || '',
      recordIDs: Array.isArray(recordIDs) ? recordIDs.slice() : [],
      children: [],
      description: String(options?.description || ''),
      meta: String(options?.meta || '')
    };
  }

  function addFolderNode(index, node) {
    index.set(node.path, node);
    if (node.parentPath) {
      const parent = index.get(node.parentPath);
      if (parent) {
        parent.children.push(node.path);
      }
    }
    return node;
  }

  function buildFolderTree(records) {
    const index = new Map();
    const selectedVault = activeVault();
    const allRecordIDs = records.map(function(record) {
      return record.id;
    });

    addFolderNode(index, createFolderNode(ROOT_FOLDER_PATH, selectedVault?.name || 'Vault', '', allRecordIDs, {
      description: selectedVault?.description || 'Encrypted folder root'
    }));

    addFolderNode(index, createFolderNode(TYPE_FOLDER_PATH, 'Types', ROOT_FOLDER_PATH, allRecordIDs, {
      description: 'Virtual folders derived from vault entry types'
    }));

    addFolderNode(index, createFolderNode(WORKSPACE_FOLDER_PATH, 'Workspaces', ROOT_FOLDER_PATH, allRecordIDs, {
      description: 'Virtual folders derived from workspace scope'
    }));

    const standardTypes = ['personal_note', 'email_snippet', 'secret'];
    const discoveredTypes = Array.from(new Set(records.map(function(record) {
      return normalizeRecordType(record.type);
    }))).filter(function(type) {
      return standardTypes.indexOf(type) === -1;
    }).sort(function(a, b) {
      return a.localeCompare(b, undefined, { sensitivity: 'base' });
    });

    standardTypes.concat(discoveredTypes).forEach(function(type) {
      const recordIDs = records.filter(function(record) {
        return normalizeRecordType(record.type) === type;
      }).map(function(record) {
        return record.id;
      });

      addFolderNode(index, createFolderNode(folderPathForType(type), recordTypeFolderName(type), TYPE_FOLDER_PATH, recordIDs, {
        description: 'Encrypted ' + recordTypeFolderName(type).toLowerCase() + ' folder'
      }));
    });

    const globalRecordIDs = records.filter(function(record) {
      return !String(record.workspace_id || '').trim();
    }).map(function(record) {
      return record.id;
    });

    addFolderNode(index, createFolderNode(folderPathForWorkspace(''), 'Global', WORKSPACE_FOLDER_PATH, globalRecordIDs, {
      description: 'Entries without workspace scope'
    }));

    const workspaceIDs = Array.from(new Set(records.map(function(record) {
      return String(record.workspace_id || '').trim();
    }).filter(Boolean))).sort(function(a, b) {
      return a.localeCompare(b, undefined, { sensitivity: 'base' });
    });

    workspaceIDs.forEach(function(workspaceID) {
      const recordIDs = records.filter(function(record) {
        return String(record.workspace_id || '').trim() === workspaceID;
      }).map(function(record) {
        return record.id;
      });

      addFolderNode(index, createFolderNode(folderPathForWorkspace(workspaceID), workspaceID, WORKSPACE_FOLDER_PATH, recordIDs, {
        description: 'Entries scoped to workspace ' + workspaceID
      }));
    });

    return index;
  }

  function rebuildFolderTree() {
    state.folderIndex = buildFolderTree(state.records);
    state.expandedFolderPaths.add(ROOT_FOLDER_PATH);
    state.expandedFolderPaths.add(TYPE_FOLDER_PATH);
    state.expandedFolderPaths.add(WORKSPACE_FOLDER_PATH);

    if (!state.folderIndex.has(state.selectedFolderPath)) {
      state.selectedFolderPath = ROOT_FOLDER_PATH;
    }
  }

  function selectedFolder() {
    return state.folderIndex.get(state.selectedFolderPath) || state.folderIndex.get(ROOT_FOLDER_PATH) || null;
  }

  function recordByID(recordID) {
    return state.recordIndex.get(recordID) || null;
  }

  function recordsForFolder(folder) {
    if (!folder || !Array.isArray(folder.recordIDs)) {
      return [];
    }

    return folder.recordIDs.map(recordByID).filter(Boolean);
  }

  function filteredRecords(records) {
    const query = String(elements?.searchInput?.value || '').trim().toLowerCase();
    if (!query) {
      return records.slice();
    }

    return records.filter(function(record) {
      const haystack = [
        record.label,
        record.type,
        record.workspace_id,
        record.source,
        Array.isArray(record.tags) ? record.tags.join(' ') : ''
      ].join(' ').toLowerCase();

      return haystack.includes(query);
    });
  }

  function folderAncestorChain(folderPath) {
    const chain = [];
    let current = state.folderIndex.get(folderPath);

    while (current) {
      chain.unshift(current);
      current = current.parentPath ? state.folderIndex.get(current.parentPath) : null;
    }

    return chain;
  }

  function folderPathLabel(folder) {
    return folderAncestorChain(folder?.path || ROOT_FOLDER_PATH).map(function(node) {
      return node.name;
    }).join(' / ');
  }

  function folderRowMeta(node) {
    const count = Array.isArray(node.recordIDs) ? node.recordIDs.length : 0;
    return String(count) + ' ' + (count === 1 ? 'file' : 'files');
  }

  function renderFolderTreeNode(nodePath, depth, forceExpanded) {
    const node = state.folderIndex.get(nodePath);
    if (!node) {
      return '';
    }

    const hasChildren = Array.isArray(node.children) && node.children.length > 0;
    const canToggle = hasChildren && node.path !== ROOT_FOLDER_PATH;
    const isExpanded = hasChildren && (forceExpanded || state.expandedFolderPaths.has(node.path));
    const isSelected = state.selectedFolderPath === node.path;

    const toggle = canToggle
      ? (
        '<button type="button" class="vault-modal-folder-toggle' + (isExpanded ? ' is-expanded' : '') + '" data-action="toggle-folder" data-folder-path="' + escapeHTML(node.path) + '" aria-label="' + (isExpanded ? 'Collapse folder' : 'Expand folder') + '">' +
          '<svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8.59,16.59L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.59Z"/></svg>' +
        '</button>'
      )
      : '<span class="vault-modal-folder-spacer"></span>';

    const childrenHTML = hasChildren && isExpanded
      ? '<div class="vault-modal-folder-children">' + node.children.map(function(childPath) {
        return renderFolderTreeNode(childPath, depth + 1, false);
      }).join('') + '</div>'
      : '';

    return (
      '<div class="vault-modal-folder-node' + (isSelected ? ' is-selected' : '') + '">' +
        '<div class="vault-modal-folder-row" style="--vault-tree-depth:' + String(depth) + ';">' +
          toggle +
          '<button type="button" class="vault-modal-folder-main" data-action="select-folder" data-folder-path="' + escapeHTML(node.path) + '">' +
            '<span class="vault-modal-folder-icon">' + folderIcon() + '</span>' +
            '<span class="vault-modal-folder-label">' + escapeHTML(node.name) + '</span>' +
            '<span class="vault-modal-folder-meta">' + escapeHTML(folderRowMeta(node)) + '</span>' +
          '</button>' +
        '</div>' +
        childrenHTML +
      '</div>'
    );
  }

  function renderFolderTree() {
    if (!elements?.folderTree) {
      return;
    }

    if (!state.status) {
      elements.folderTree.innerHTML = '<div class="vault-modal-empty">Checking vault availability.</div>';
      return;
    }

    if (!state.status.available) {
      elements.folderTree.innerHTML = '<div class="vault-modal-empty">No vault exists yet. Create your first encrypted vault to unlock the folder explorer.</div>';
      return;
    }

    if (state.status.locked) {
      elements.folderTree.innerHTML = '<div class="vault-modal-empty">Unlock the vault to browse the encrypted folder tree.</div>';
      return;
    }

    const root = state.folderIndex.get(ROOT_FOLDER_PATH);
    if (!root) {
      elements.folderTree.innerHTML = '<div class="vault-modal-empty">No vault folders available yet.</div>';
      return;
    }

    elements.folderTree.innerHTML = '<div class="vault-modal-folder-tree-scroll">' + renderFolderTreeNode(ROOT_FOLDER_PATH, 0, true) + '</div>';
  }

  function renderFolderBreadcrumb() {
    if (!elements?.breadcrumb) {
      return;
    }

    if (!state.status || state.status.locked) {
      elements.breadcrumb.innerHTML = '';
      return;
    }

    const chain = folderAncestorChain(state.selectedFolderPath);
    elements.breadcrumb.innerHTML = chain.map(function(node, index) {
      const separator = index === 0 ? '' : '<span class="vault-modal-folder-crumb-separator">/</span>';
      const active = index === chain.length - 1;

      if (active) {
        return separator + '<span class="vault-modal-folder-crumb is-active">' + escapeHTML(node.name) + '</span>';
      }

      return (
        separator +
        '<button type="button" class="vault-modal-folder-crumb" data-action="select-folder" data-folder-path="' + escapeHTML(node.path) + '">' +
          escapeHTML(node.name) +
        '</button>'
      );
    }).join('');
  }

  function renderSelectedRecordDetail() {
    if (!state.selectedRecord) {
      return (
        '<div class="vault-modal-preview-detail-empty">' +
          'Select a file in this encrypted folder to inspect its metadata and reveal its protected payload.' +
        '</div>'
      );
    }

    const record = state.selectedRecord;
    const tags = Array.isArray(record.tags) && record.tags.length > 0
      ? record.tags.map(function(tag) {
        return '<span class="vault-modal-chip">' + escapeHTML(tag) + '</span>';
      }).join('')
      : '<span class="vault-modal-chip is-muted">No tags</span>';

    const revealLabel = state.payloadRevealed ? 'Hide Payload' : 'Reveal Payload';
    const payloadClass = 'vault-modal-payload-preview' + (state.payloadRevealed ? '' : ' is-concealed');

    return (
      '<div class="vault-modal-detail">' +
        '<div class="vault-modal-detail-header">' +
          '<div>' +
            '<div class="vault-modal-detail-title">' + escapeHTML(record.label || 'Untitled entry') + '</div>' +
            '<div class="vault-modal-detail-meta">' + escapeHTML([recordTypeLabel(record.type), record.workspace_id].filter(Boolean).join(' • ') || 'Vault file') + '</div>' +
          '</div>' +
          '<button type="button" class="modern-btn modern-btn-secondary" data-action="toggle-payload">' + escapeHTML(revealLabel) + '</button>' +
        '</div>' +
        '<div class="vault-modal-chip-row">' + tags + '</div>' +
        '<div class="vault-modal-detail-grid">' +
          '<div class="vault-modal-detail-item"><span>Type</span><strong>' + escapeHTML(recordTypeLabel(record.type)) + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Workspace</span><strong>' + escapeHTML(record.workspace_id || 'Global') + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Source</span><strong>' + escapeHTML(record.source || 'Not set') + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Retention</span><strong>' + escapeHTML(record.retention_policy || 'Not set') + '</strong></div>' +
          '<div class="vault-modal-detail-item vault-modal-detail-item-wide"><span>Updated</span><strong>' + escapeHTML(prettyDate(record.updated_at || record.created_at)) + '</strong></div>' +
        '</div>' +
        '<div class="vault-modal-payload-wrap">' +
          '<div class="vault-modal-payload-label">Protected payload</div>' +
          '<pre class="' + payloadClass + '">' + escapeHTML(prettyPayload(record.payload)) + '</pre>' +
        '</div>' +
      '</div>'
    );
  }

  function renderExplorerPreview() {
    if (!elements?.preview || !elements?.recordsSummary) {
      return;
    }

    if (!state.status) {
      elements.recordsSummary.textContent = 'Waiting for vault status...';
      elements.preview.innerHTML = '<div class="vault-modal-empty">Checking vault availability.</div>';
      return;
    }

    if (!state.status.available) {
      elements.recordsSummary.textContent = 'No vault selected';
      elements.preview.innerHTML = '<div class="vault-modal-empty">Create a vault to start saving encrypted files and browsing them in the folder explorer.</div>';
      return;
    }

    if (state.status.locked) {
      elements.recordsSummary.textContent = 'Vault is locked';
      elements.preview.innerHTML = '<div class="vault-modal-empty">Unlock the vault to browse files inside the encrypted folder.</div>';
      return;
    }

    const folder = selectedFolder();
    if (!folder) {
      elements.recordsSummary.textContent = 'No folder selected';
      elements.preview.innerHTML = '<div class="vault-modal-empty">Select a folder to continue.</div>';
      return;
    }

    const folderRecords = recordsForFolder(folder);
    const visibleRecords = filteredRecords(folderRecords);

    if (state.selectedRecord && !folderRecords.some(function(record) { return record.id === state.selectedRecord.id; })) {
      clearSelection();
    }

    elements.recordsSummary.textContent = String(visibleRecords.length) + ' of ' + String(folderRecords.length) + ' ' + (folderRecords.length === 1 ? 'file' : 'files') + ' in ' + folder.name;

    const filesHTML = visibleRecords.length > 0
      ? visibleRecords.map(function(record) {
        const selectedClass = state.selectedRecord && state.selectedRecord.id === record.id ? ' is-selected' : '';
        const subtitle = [recordTypeLabel(record.type), record.workspace_id || 'Global'].join(' • ');
        const tags = Array.isArray(record.tags) ? record.tags.length : 0;

        return (
          '<button type="button" class="vault-modal-preview-file' + selectedClass + '" data-action="select-record" data-record-id="' + escapeHTML(record.id) + '">' +
            '<span class="vault-modal-preview-file-icon">' + fileTypeIcon(record.type) + '</span>' +
            '<span class="vault-modal-preview-file-main">' +
              '<span class="vault-modal-preview-file-name">' + escapeHTML(record.label || 'Untitled entry') + '</span>' +
              '<span class="vault-modal-preview-file-meta">' + escapeHTML(subtitle) + '</span>' +
            '</span>' +
            '<span class="vault-modal-preview-file-side">' +
              '<span class="vault-modal-preview-file-updated">' + escapeHTML(prettyDate(record.updated_at || record.created_at)) + '</span>' +
              '<span class="vault-modal-chip">' + String(tags) + ' ' + (tags === 1 ? 'tag' : 'tags') + '</span>' +
            '</span>' +
          '</button>'
        );
      }).join('')
      : '<div class="vault-modal-preview-empty-inline">No files match this folder and search combination yet.</div>';

    elements.preview.innerHTML = (
      '<div class="vault-modal-preview-header">' +
        '<div class="vault-modal-preview-title">' + escapeHTML(folder.name) + '</div>' +
        '<div class="vault-modal-preview-subtitle">' + escapeHTML(folderPathLabel(folder)) + '</div>' +
      '</div>' +
      '<div class="vault-modal-preview-stats">' +
        '<span class="vault-modal-chip">' + String(folderRecords.length) + ' ' + (folderRecords.length === 1 ? 'file' : 'files') + '</span>' +
        '<span class="vault-modal-chip is-muted">' + escapeHTML(folder.description || 'Encrypted folder view') + '</span>' +
      '</div>' +
      '<div class="vault-modal-preview-file-list">' + filesHTML + '</div>' +
      renderSelectedRecordDetail()
    );
  }

  function renderExplorer() {
    renderFolderTree();
    renderFolderBreadcrumb();
    renderExplorerPreview();
  }

  function selectFolder(folderPath) {
    if (!folderPath || !state.folderIndex.has(folderPath)) {
      return;
    }

    state.selectedFolderPath = folderPath;
    renderExplorer();
  }

  function toggleFolder(folderPath) {
    if (!folderPath || !state.folderIndex.has(folderPath) || folderPath === ROOT_FOLDER_PATH) {
      return;
    }

    if (state.expandedFolderPaths.has(folderPath)) {
      state.expandedFolderPaths.delete(folderPath);
    } else {
      state.expandedFolderPaths.add(folderPath);
    }

    renderFolderTree();
  }

  async function loadVaultStatus() {
    if (!activeVaultID()) {
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
    if (!state.status || !state.status.available || state.status.locked) {
      state.records = [];
      state.recordIndex = new Map();
      rebuildFolderTree();
      renderExplorer();
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/records'));
    state.records = Array.isArray(data.records) ? data.records : [];
    state.recordIndex = new Map(state.records.map(function(record) {
      return [record.id, record];
    }));
    rebuildFolderTree();
    renderExplorer();
  }

  async function selectRecord(recordID) {
    if (!recordID) {
      clearSelection();
      renderExplorerPreview();
      return;
    }

    try {
      const record = await apiRequest(vaultURL('/api/vault/records/' + encodeURIComponent(recordID)));
      renderSelection(record);
    } catch (error) {
      console.error('Failed to load vault record:', error);
      showAlert(error.message || 'Failed to load vault record.', 'error');
    }
  }

  async function refreshVault(preserveSelection) {
    const selectedID = preserveSelection === false ? '' : state.selectedRecord?.id;

    try {
      showAlert('');
      await loadVaults(preserveSelection);
      const status = await loadVaultStatus();
      if (!status.available) {
        state.records = [];
        state.recordIndex = new Map();
        rebuildFolderTree();
        clearSelection();
        renderExplorer();
        renderVaultSelector();
        return;
      }

      if (!status.locked) {
        await loadVaultRecords();
        if (selectedID && state.recordIndex.has(selectedID)) {
          await selectRecord(selectedID);
        } else {
          clearSelection();
          renderExplorerPreview();
        }
      } else {
        state.records = [];
        state.recordIndex = new Map();
        rebuildFolderTree();
        clearSelection();
        renderExplorer();
        renderVaultSelector();
      }
    } catch (error) {
      console.error('Failed to refresh vault:', error);
      showAlert(error.message || 'Failed to refresh vault.', 'error');
    }
  }

  function focusPrimaryControl() {
    if (!elements || !elements.modal.classList.contains('show') || state.isHydrating || !state.hasHydrated) {
      return;
    }

    if (state.unlockDialogOpen && !elements.unlockOverlay?.hidden) {
      elements.passwordInput?.focus();
      return;
    }

    if (!state.vaults.length) {
      elements.newVaultNameInput?.focus();
      return;
    }
    if (state.status && state.status.locked) {
      elements.selectedVaultUnlockBtn?.focus();
      return;
    }
    elements.searchInput?.focus();
  }

  async function hydrateModal() {
    if (state.isHydrating) {
      return;
    }

    state.isHydrating = true;
    applyModalMode();
    try {
      await refreshVault();
      state.hasHydrated = true;
    } finally {
      state.isHydrating = false;
      applyModalMode();
      focusPrimaryControl();
    }
  }

  async function unlockVault() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before unlocking storage access.', 'warning');
      return;
    }

    if (!String(elements.passwordInput?.value || '').trim()) {
      showAlert('Enter the selected vault password before unlocking.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.unlockBtn, true, 'Unlocking');
      await apiRequest(vaultURL('/api/vault/unlock'), {
        method: 'POST',
        body: {
          vault_id: activeVaultID(),
          vault_password: String(elements.passwordInput?.value || '')
        }
      });
      if (elements.passwordInput) {
        elements.passwordInput.value = '';
      }
      notify('Vault unlocked.', 'success');
      closeUnlockDialog({ restoreFocus: false });
      await refreshVault(false);
    } catch (error) {
      console.error('Failed to unlock vault:', error);
      showAlert(error.message || 'Failed to unlock vault.', 'error');
    } finally {
      setButtonLoading(elements.unlockBtn, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  async function lockVault() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before changing vault access.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.lockBtn, true, 'Locking');
      await apiRequest(vaultURL('/api/vault/lock'), {
        method: 'POST',
        body: {}
      });
      notify('Vault locked.', 'success');
      closeUnlockDialog({ restoreFocus: false });
      await refreshVault(false);
    } catch (error) {
      console.error('Failed to lock vault:', error);
      showAlert(error.message || 'Failed to lock vault.', 'error');
    } finally {
      setButtonLoading(elements.lockBtn, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  async function createRecord() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before saving an entry.', 'warning');
      return;
    }

    if (!state.status || state.status.locked) {
      showAlert('Unlock the vault before creating a new entry.', 'warning');
      return;
    }

    const body = {
      vault_id: activeVaultID(),
      type: String(elements.entryTypeInput?.value || ''),
      workspace_id: String(elements.entryWorkspaceInput?.value || '').trim(),
      label: String(elements.entryLabelInput?.value || '').trim(),
      tags: parseTags(elements.entryTagsInput?.value),
      source: String(elements.entrySourceInput?.value || '').trim(),
      retention_policy: String(elements.entryRetentionInput?.value || '').trim()
    };

    if (!body.label) {
      showAlert('Label is required before saving to the vault.', 'warning');
      return;
    }

    try {
      body.payload = parsePayloadInput();
    } catch (error) {
      showAlert(error.message || 'Payload must be valid JSON.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.saveBtn, true, 'Saving entry');
      const response = await apiRequest('/api/vault/records', {
        method: 'POST',
        body: body
      });

      resetCreateForm();
      state.selectedFolderPath = ROOT_FOLDER_PATH;
      notify('Vault entry saved.', 'success');
      await refreshVault(false);

      const recordID = response?.record?.id;
      if (recordID) {
        await selectRecord(recordID);
      }
    } catch (error) {
      console.error('Failed to save vault entry:', error);
      showAlert(error.message || 'Failed to save vault entry.', 'error');
    } finally {
      setButtonLoading(elements.saveBtn, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  function bindFolderTreeEvents() {
    elements.folderTree?.addEventListener('click', function(event) {
      const target = event.target.closest('[data-action]');
      if (!target) {
        return;
      }

      const action = target.getAttribute('data-action');
      const folderPath = target.getAttribute('data-folder-path');

      if (action === 'toggle-folder' && folderPath) {
        toggleFolder(folderPath);
        return;
      }

      if (action === 'select-folder' && folderPath) {
        selectFolder(folderPath);
      }
    });
  }

  function bindPreviewEvents() {
    elements.preview?.addEventListener('click', function(event) {
      const target = event.target.closest('[data-action]');
      if (!target) {
        return;
      }

      const action = target.getAttribute('data-action');

      if (action === 'select-record') {
        const recordID = target.getAttribute('data-record-id');
        if (recordID) {
          selectRecord(recordID);
        }
        return;
      }

      if (action === 'toggle-payload') {
        togglePayloadReveal();
      }
    });
  }

  function bindBreadcrumbEvents() {
    elements.breadcrumb?.addEventListener('click', function(event) {
      const target = event.target.closest('[data-action="select-folder"]');
      if (!target) {
        return;
      }

      const folderPath = target.getAttribute('data-folder-path');
      if (folderPath) {
        selectFolder(folderPath);
      }
    });
  }

  function bindEvents() {
    elements.togglePasswordBtn?.addEventListener('click', function() {
      if (!elements.passwordInput) {
        return;
      }

      const nextType = elements.passwordInput.type === 'password' ? 'text' : 'password';
      elements.passwordInput.type = nextType;
    });

    elements.passwordInput?.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        unlockVault();
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        closeUnlockDialog();
      }
    });

    elements.selectedVaultUnlockBtn?.addEventListener('click', function() {
      openUnlockDialog();
    });

    elements.unlockBtn?.addEventListener('click', function() {
      unlockVault();
    });

    elements.unlockCancelBtn?.addEventListener('click', function() {
      closeUnlockDialog();
    });

    elements.unlockOverlay?.addEventListener('click', function(event) {
      const dismissTrigger = event.target.closest('[data-action="dismiss-unlock-dialog"]');
      if (dismissTrigger) {
        closeUnlockDialog();
      }
    });

    elements.lockBtn?.addEventListener('click', function() {
      lockVault();
    });

    elements.refreshBtn?.addEventListener('click', function() {
      refreshVault();
    });

    elements.vaultSelect?.addEventListener('change', function(event) {
      switchVault(event.target.value, { promptUnlock: true });
    });

    elements.vaultRail?.addEventListener('click', function(event) {
      const trigger = event.target.closest('[data-action="select-vault-chip"]');
      if (!trigger) {
        return;
      }

      switchVault(trigger.getAttribute('data-vault-id'), { promptUnlock: true });
    });

    elements.renameVaultBtn?.addEventListener('click', function() {
      updateVault();
    });

    elements.deleteVaultBtn?.addEventListener('click', function() {
      deleteVault();
    });

    elements.createVaultBtn?.addEventListener('click', function() {
      createVault();
    });

    elements.editVaultNameInput?.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        updateVault();
      }
    });

    elements.newVaultNameInput?.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        createVault();
      }
    });

    elements.confirmVaultPasswordInput?.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        createVault();
      }
    });

    elements.resetBtn?.addEventListener('click', function() {
      resetCreateForm();
      showAlert('');
    });

    elements.saveBtn?.addEventListener('click', function() {
      createRecord();
    });

    elements.searchInput?.addEventListener('input', function() {
      renderExplorerPreview();
    });

    bindFolderTreeEvents();
    bindPreviewEvents();
    bindBreadcrumbEvents();

    elements.modal?.addEventListener('keydown', function(event) {
      if (event.key === 'Escape' && state.unlockDialogOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeUnlockDialog();
      }
    });

    if (window.bootstrap && window.bootstrap.Modal) {
      elements.modal.addEventListener('show.bs.modal', function() {
        setLauncherActive(true);
        if (!state.hasHydrated) {
          hydrateModal();
          return;
        }
        refreshVault();
      });

      elements.modal.addEventListener('hidden.bs.modal', function() {
        setLauncherActive(false);
        closeUnlockDialog();
      });

      elements.modal.addEventListener('shown.bs.modal', function() {
        focusPrimaryControl();
      });
    }
  }

  async function init() {
    elements = getElements();
    if (!elements) {
      return;
    }

    resetCreateForm();
    rebuildFolderTree();
    applyModalMode();
    renderExplorer();
    bindEvents();
    updateLauncherCount(null);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

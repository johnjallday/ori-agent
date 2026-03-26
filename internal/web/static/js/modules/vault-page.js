(function() {
  const DEFAULT_VAULT_ID = '';
  const VAULT_STORAGE_KEY = 'ori-selected-vault-id';
  const ROOT_FOLDER_PATH = 'vault';
  const TYPE_FOLDER_PATH = 'types';
  const WORKSPACE_FOLDER_PATH = 'workspaces';
  const DEFAULT_EXPANDED_FOLDERS = new Set([ROOT_FOLDER_PATH, TYPE_FOLDER_PATH, WORKSPACE_FOLDER_PATH]);
  const TYPE_META = {
    personal_note: {
      label: 'Personal Note',
      folderName: 'Notes'
    },
    email_snippet: {
      label: 'Email Snippet',
      folderName: 'Email'
    },
    secret: {
      label: 'Secret',
      folderName: 'Secrets'
    }
  };
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
  const entrySourceInput = document.getElementById('vaultEntrySource');
  const entryRetentionInput = document.getElementById('vaultEntryRetention');
  const entryPayloadInput = document.getElementById('vaultEntryPayload');
  const saveEntryBtn = document.getElementById('vaultSaveEntryBtn');
  const resetEntryBtn = document.getElementById('vaultResetEntryBtn');
  const revealPayloadBtn = document.getElementById('vaultRevealPayloadBtn');
  const deleteEntryBtn = document.getElementById('vaultDeleteEntryBtn');
  const recordsListEl = document.getElementById('vaultRecordsList');
  const selectionBadge = document.getElementById('vaultSelectionBadge');
  const searchInput = document.getElementById('vaultPageSearchInput');
  const recordsSummaryEl = document.getElementById('vaultPageRecordsSummary');
  const folderVaultTabsEl = document.getElementById('vaultPageFolderVaultTabs');
  const folderBreadcrumbEl = document.getElementById('vaultPageFolderBreadcrumb');
  const folderTreeEl = document.getElementById('vaultPageFolderTree');
  const explorerPreviewEl = document.getElementById('vaultPageExplorerPreview');

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
  let recordIndex = new Map();
  let folderIndex = new Map();
  let expandedFolderPaths = new Set(DEFAULT_EXPANDED_FOLDERS);
  let selectedFolderPath = ROOT_FOLDER_PATH;

  function notify(message, type = 'info', options = {}) {
    if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, type, options);
      return;
    }
    if (window.Toast) {
      const normalized = type === 'danger' ? 'error' : type === 'warn' ? 'warning' : type;
      const fn = window.Toast[normalized];
      if (typeof fn === 'function') {
        fn(message, options);
        return;
      }
    }
    console.log(`[vault:${type}]`, message);
  }

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

  function formatBytes(value) {
    const bytes = Number(value || 0);
    if (!Number.isFinite(bytes) || bytes <= 0) {
      return '0 B';
    }

    const units = ['B', 'KB', 'MB', 'GB'];
    let size = bytes;
    let unitIndex = 0;
    while (size >= 1024 && unitIndex < units.length - 1) {
      size /= 1024;
      unitIndex += 1;
    }

    const digits = size >= 10 || unitIndex === 0 ? 0 : 1;
    return `${size.toFixed(digits)} ${units[unitIndex]}`;
  }

  function attachmentKindForMimeType(mimeType) {
    return String(mimeType || '').toLowerCase().startsWith('image/') ? 'image' : 'file';
  }

  function attachmentIcon(kind) {
    if (kind === 'image') {
      return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21,19V5A2,2 0 0,0 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19M8.5,11A1.5,1.5 0 0,1 10,12.5A1.5,1.5 0 0,1 8.5,14A1.5,1.5 0 0,1 7,12.5A1.5,1.5 0 0,1 8.5,11M5,19L8,15L10.5,18L14,13.5L19,19H5Z"/></svg>';
    }

    return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';
  }

  function generateAttachmentID() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return window.crypto.randomUUID();
    }

    return `attachment-${Date.now()}-${String(Math.random()).slice(2, 8)}`;
  }

  function normalizeEntryAttachment(item) {
    if (!item || typeof item !== 'object') {
      return null;
    }

    const name = String(item.name || '').trim();
    const contentBase64 = String(item.content_base64 || item.base64_data || '').trim();
    if (!name || !contentBase64) {
      return null;
    }

    const mimeType = String(item.mime_type || item.mimeType || 'application/octet-stream').trim() || 'application/octet-stream';
    const sizeBytes = Number(item.size_bytes ?? item.size ?? 0);

    return {
      id: String(item.id || generateAttachmentID()),
      name,
      mime_type: mimeType,
      size_bytes: Number.isFinite(sizeBytes) && sizeBytes >= 0 ? sizeBytes : 0,
      kind: String(item.kind || '').trim() || attachmentKindForMimeType(mimeType),
      content_base64: contentBase64
    };
  }

  function entryAttachmentsFromPayload(payload) {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return [];
    }

    return Array.isArray(payload.attachments)
      ? payload.attachments.map(normalizeEntryAttachment).filter(Boolean)
      : [];
  }

  function payloadWithoutAttachments(payload) {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return payload;
    }

    const next = {};
    Object.keys(payload).forEach((key) => {
      if (key !== 'attachments') {
        next[key] = payload[key];
      }
    });
    return next;
  }

  function payloadForPreview(payload) {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return payload;
    }

    const next = payloadWithoutAttachments(payload);
    const attachments = entryAttachmentsFromPayload(payload);
    if (attachments.length) {
      next.attachments = attachments.map((item) => ({
        id: item.id,
        name: item.name,
        mime_type: item.mime_type,
        size_bytes: item.size_bytes,
        kind: item.kind
      }));
    }
    return next;
  }

  function attachmentDataURL(attachment) {
    const mimeType = String(attachment?.mime_type || 'application/octet-stream').trim() || 'application/octet-stream';
    const contentBase64 = String(attachment?.content_base64 || '').trim();
    return contentBase64 ? `data:${mimeType};base64,${contentBase64}` : '';
  }

  function downloadAttachment(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      showInlineAlert('That attachment could not be opened.', 'warning');
      return;
    }

    const link = document.createElement('a');
    link.href = attachmentDataURL(normalized);
    link.download = normalized.name || 'vault-attachment';
    link.style.display = 'none';
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
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
    return `type::${normalizeRecordType(type)}`;
  }

  function folderPathForWorkspace(workspaceID) {
    const normalized = String(workspaceID || '').trim();
    return normalized ? `workspace::${normalized}` : 'workspace::global';
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
    return `<span style="display:inline-block;width:0.95rem;height:0.95rem;border-radius:999px;background:${color};box-shadow:0 0 0 6px rgba(255,255,255,0.82);"></span>`;
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
      <div class="alert ${classes[type] || classes.info}" role="alert">
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
      renderFolderVaultTabs();
      return;
    }

    selectedVaultID = nextVaultID;
    writeStoredVaultID(selectedVaultID);
    selectedFolderPath = ROOT_FOLDER_PATH;
    clearRecordForm({ refreshList: false, refreshExplorer: false });
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
      selectedFolderPath = ROOT_FOLDER_PATH;
      clearRecordForm({ refreshList: false, refreshExplorer: false });
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
      selectedFolderPath = ROOT_FOLDER_PATH;
      clearRecordForm({ refreshList: false, refreshExplorer: false });
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
    if (searchInput) {
      searchInput.disabled = !vaultStatus?.available || Boolean(locked);
    }
    renderFolderVaultTabs();

    if (disableVaultEditing) {
      records = [];
      recordIndex = new Map();
      rebuildFolderTree();
      recordsListEl.innerHTML = vaultStatus?.available
        ? '<div class="vault-page-empty">Unlock the vault to browse saved entries.</div>'
        : '<div class="vault-page-empty">Create a vault to begin storing encrypted entries.</div>';
      clearRecordForm({ preserveStatus: true, refreshList: false, refreshExplorer: false });
      renderExplorer();
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
    const refreshList = options.refreshList !== false;
    const refreshExplorer = options.refreshExplorer !== false;

    selectedRecord = null;
    payloadRevealed = false;
    selectionBadge.textContent = 'No selection';
    entryTypeInput.value = 'personal_note';
    entryWorkspaceInput.value = '';
    entryLabelInput.value = '';
    entryTagsInput.value = '';
    entrySourceInput.value = '';
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

    if (refreshList) {
      renderRecordsList(records);
    }
    if (refreshExplorer) {
      renderExplorerPreview();
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
    entrySourceInput.value = record.source || '';
    entryRetentionInput.value = record.retention_policy || '';
    entryPayloadInput.disabled = true;
    entryPayloadInput.style.filter = 'blur(6px)';
    entryPayloadInput.value = '{\n  "locked": true\n}';
    saveEntryBtn.textContent = 'Save Changes';
    revealPayloadBtn.textContent = 'Reveal Payload';
    revealPayloadBtn.classList.remove('d-none');
    deleteEntryBtn.classList.remove('d-none');
    setInteractiveState(Boolean(vaultStatus?.locked));
    renderRecordsList(records);
    renderExplorerPreview();
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
      renderExplorerPreview();
      return;
    }

    entryPayloadInput.disabled = true;
    entryPayloadInput.style.filter = 'blur(6px)';
    entryPayloadInput.value = '{\n  "locked": true\n}';
    revealPayloadBtn.textContent = 'Reveal Payload';
    renderExplorerPreview();
  }

  function renderRecordsList(items = records) {
    records = Array.isArray(items) ? items : [];

    if (!records.length) {
      recordsListEl.innerHTML = '<div class="vault-page-empty">No vault entries saved yet.</div>';
      return;
    }

    recordsListEl.innerHTML = records.map((record) => {
      const isSelected = selectedRecord?.id === record.id ? ' is-selected' : '';
      const subtitle = `${escapeHTML(recordTypeLabel(record.type))}${record.workspace_id ? ` • ${escapeHTML(record.workspace_id)}` : ''}`;
      const tagCount = Array.isArray(record.tags) ? record.tags.length : 0;
      return `
        <button type="button" class="vault-page-record${isSelected}" data-record-id="${escapeHTML(record.id)}">
          <div class="vault-page-record-row">
            <div>
              <div class="vault-page-record-title">${escapeHTML(record.label || record.type || 'Untitled')}</div>
              <span class="vault-page-record-meta">${subtitle}</span>
            </div>
            <span class="vault-page-record-tag">${tagCount} ${tagCount === 1 ? 'tag' : 'tags'}</span>
          </div>
        </button>
      `;
    }).join('');
  }

  function vaultLockIcon(locked) {
    if (locked) {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12,1A5,5 0 0,1 17,6V8H18A2,2 0 0,1 20,10V21A2,2 0 0,1 18,23H6A2,2 0 0,1 4,21V10A2,2 0 0,1 6,8H7V6A5,5 0 0,1 12,1M12,3A3,3 0 0,0 9,6V8H15V6A3,3 0 0,0 12,3M12,12A2,2 0 0,0 10,14A2,2 0 0,0 11,15.73V18H13V15.73A2,2 0 0,0 14,14A2,2 0 0,0 12,12Z"/></svg>';
    }

    return '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4.5" y="10" width="15" height="10" rx="2.5"></rect><path d="M9 10V7.6A3.6 3.6 0 0 1 15.3 5.2"></path><path d="M15.2 5.2L18 7.8"></path><path d="M12 14.2V16.2"></path></svg>';
  }

  function createFolderNode(path, name, parentPath, recordIDs, options) {
    return {
      path,
      name,
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

  function buildFolderTree(items) {
    const index = new Map();
    const activeVault = currentVault();
    const allRecordIDs = items.map((record) => record.id);

    addFolderNode(index, createFolderNode(ROOT_FOLDER_PATH, activeVault?.name || 'Vault', '', allRecordIDs, {
      description: activeVault?.description || 'Encrypted folder root'
    }));

    addFolderNode(index, createFolderNode(TYPE_FOLDER_PATH, 'Types', ROOT_FOLDER_PATH, allRecordIDs, {
      description: 'Virtual folders derived from vault entry types'
    }));

    addFolderNode(index, createFolderNode(WORKSPACE_FOLDER_PATH, 'Workspaces', ROOT_FOLDER_PATH, allRecordIDs, {
      description: 'Virtual folders derived from workspace scope'
    }));

    const standardTypes = ['personal_note', 'email_snippet', 'secret'];
    const discoveredTypes = Array.from(new Set(items.map((record) => normalizeRecordType(record.type))))
      .filter((type) => !standardTypes.includes(type))
      .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

    standardTypes.concat(discoveredTypes).forEach((type) => {
      const recordIDs = items
        .filter((record) => normalizeRecordType(record.type) === type)
        .map((record) => record.id);

      addFolderNode(index, createFolderNode(folderPathForType(type), recordTypeFolderName(type), TYPE_FOLDER_PATH, recordIDs, {
        description: `Encrypted ${recordTypeFolderName(type).toLowerCase()} folder`
      }));
    });

    const globalRecordIDs = items
      .filter((record) => !String(record.workspace_id || '').trim())
      .map((record) => record.id);

    addFolderNode(index, createFolderNode(folderPathForWorkspace(''), 'Global', WORKSPACE_FOLDER_PATH, globalRecordIDs, {
      description: 'Entries without workspace scope'
    }));

    const workspaceIDs = Array.from(new Set(items.map((record) => String(record.workspace_id || '').trim()).filter(Boolean)))
      .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

    workspaceIDs.forEach((workspaceID) => {
      const recordIDs = items
        .filter((record) => String(record.workspace_id || '').trim() === workspaceID)
        .map((record) => record.id);

      addFolderNode(index, createFolderNode(folderPathForWorkspace(workspaceID), workspaceID, WORKSPACE_FOLDER_PATH, recordIDs, {
        description: `Entries scoped to workspace ${workspaceID}`
      }));
    });

    return index;
  }

  function rebuildFolderTree() {
    folderIndex = buildFolderTree(records);
    expandedFolderPaths.add(ROOT_FOLDER_PATH);
    expandedFolderPaths.add(TYPE_FOLDER_PATH);
    expandedFolderPaths.add(WORKSPACE_FOLDER_PATH);

    if (!folderIndex.has(selectedFolderPath)) {
      selectedFolderPath = ROOT_FOLDER_PATH;
    }
  }

  function selectedFolder() {
    return folderIndex.get(selectedFolderPath) || folderIndex.get(ROOT_FOLDER_PATH) || null;
  }

  function recordByID(recordID) {
    return recordIndex.get(recordID) || null;
  }

  function recordsForFolder(folder) {
    if (!folder || !Array.isArray(folder.recordIDs)) {
      return [];
    }

    return folder.recordIDs.map(recordByID).filter(Boolean);
  }

  function filteredRecords(items) {
    const query = String(searchInput?.value || '').trim().toLowerCase();
    if (!query) {
      return items.slice();
    }

    return items.filter((record) => {
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
    let current = folderIndex.get(folderPath);

    while (current) {
      chain.unshift(current);
      current = current.parentPath ? folderIndex.get(current.parentPath) : null;
    }

    return chain;
  }

  function folderPathLabel(folder) {
    return folderAncestorChain(folder?.path || ROOT_FOLDER_PATH).map((node) => node.name).join(' / ');
  }

  function renderRecordAttachmentCard(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      return '';
    }

    const thumbnail = normalized.kind === 'image'
      ? `<img class="vault-modal-attachment-thumb" src="${escapeHTML(attachmentDataURL(normalized))}" alt="${escapeHTML(normalized.name)}">`
      : `<div class="vault-modal-attachment-thumb is-generic">${attachmentIcon(normalized.kind)}</div>`;

    return `
      <div class="vault-modal-attachment-card">
        ${thumbnail}
        <div class="vault-modal-attachment-card-body">
          <div class="vault-modal-attachment-name">${escapeHTML(normalized.name)}</div>
          <div class="vault-modal-attachment-meta">${escapeHTML([normalized.kind === 'image' ? 'Image' : 'File', formatBytes(normalized.size_bytes)].join(' • '))}</div>
          <button type="button" class="modern-btn modern-btn-secondary vault-modal-attachment-download" data-action="download-attachment" data-attachment-id="${escapeHTML(normalized.id)}">Download</button>
        </div>
      </div>
    `;
  }

  function folderRowMeta(node) {
    const count = Array.isArray(node.recordIDs) ? node.recordIDs.length : 0;
    return `${count} ${count === 1 ? 'file' : 'files'}`;
  }

  function renderFolderTreeNode(nodePath, depth, forceExpanded) {
    const node = folderIndex.get(nodePath);
    if (!node) {
      return '';
    }

    const hasChildren = Array.isArray(node.children) && node.children.length > 0;
    const canToggle = hasChildren && node.path !== ROOT_FOLDER_PATH;
    const isExpanded = hasChildren && (forceExpanded || expandedFolderPaths.has(node.path));
    const isSelected = selectedFolderPath === node.path;

    const toggle = canToggle
      ? `<button type="button" class="vault-modal-folder-toggle${isExpanded ? ' is-expanded' : ''}" data-action="toggle-folder" data-folder-path="${escapeHTML(node.path)}" aria-label="${isExpanded ? 'Collapse folder' : 'Expand folder'}">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8.59,16.59L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.59Z"/></svg>
        </button>`
      : '<span class="vault-modal-folder-spacer"></span>';

    const childrenHTML = hasChildren && isExpanded
      ? `<div class="vault-modal-folder-children">${node.children.map((childPath) => renderFolderTreeNode(childPath, depth + 1, false)).join('')}</div>`
      : '';

    return `
      <div class="vault-modal-folder-node${isSelected ? ' is-selected' : ''}">
        <div class="vault-modal-folder-row" style="--vault-tree-depth:${String(depth)};">
          ${toggle}
          <button type="button" class="vault-modal-folder-main" data-action="select-folder" data-folder-path="${escapeHTML(node.path)}">
            <span class="vault-modal-folder-icon">${folderIcon()}</span>
            <span class="vault-modal-folder-label">${escapeHTML(node.name)}</span>
            <span class="vault-modal-folder-meta">${escapeHTML(folderRowMeta(node))}</span>
          </button>
        </div>
        ${childrenHTML}
      </div>
    `;
  }

  function renderFolderTree() {
    if (!folderTreeEl) {
      return;
    }

    if (!vaultStatus) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">Checking vault availability.</div>';
      return;
    }

    if (!vaultStatus.available) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">No vault exists yet. Create your first encrypted vault to unlock the folder explorer.</div>';
      return;
    }

    if (vaultStatus.locked) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">Unlock the vault to browse the encrypted folder tree.</div>';
      return;
    }

    const root = folderIndex.get(ROOT_FOLDER_PATH);
    if (!root) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">No vault folders available yet.</div>';
      return;
    }

    folderTreeEl.innerHTML = `<div class="vault-modal-folder-tree-scroll">${renderFolderTreeNode(ROOT_FOLDER_PATH, 0, true)}</div>`;
  }

  function renderFolderVaultTabs() {
    if (!folderVaultTabsEl) {
      return;
    }

    if (!vaultStatus || !vaults.length) {
      folderVaultTabsEl.hidden = true;
      folderVaultTabsEl.innerHTML = '';
      return;
    }

    folderVaultTabsEl.hidden = false;
    folderVaultTabsEl.innerHTML = vaults.map((vaultItem) => {
      const isActive = vaultItem.id === currentVaultID();
      const canToggleAccess = isActive && Boolean(vaultStatus?.available);
      const locked = canToggleAccess ? Boolean(vaultStatus?.locked) : true;
      const actionButton = canToggleAccess
        ? `<button type="button" class="vault-modal-folder-vault-tab-icon${locked ? ' is-locked' : ' is-unlocked'}" data-action="toggle-vault-access" data-vault-id="${escapeHTML(vaultItem.id)}" aria-label="${escapeHTML((locked ? 'Unlock ' : 'Lock ') + vaultDisplayLabel(vaultItem))}" title="${escapeHTML(locked ? 'Unlock vault' : 'Lock vault')}">${vaultLockIcon(locked)}</button>`
        : '';

      return `
        <div class="vault-modal-folder-vault-tab-wrap${isActive ? ' is-active' : ''}">
          <button type="button" class="vault-modal-folder-vault-tab${isActive ? ' is-active' : ''}" role="tab" aria-selected="${isActive ? 'true' : 'false'}" data-action="select-folder-vault-tab" data-vault-id="${escapeHTML(vaultItem.id)}">
            <span class="vault-modal-folder-vault-tab-label">${escapeHTML(vaultDisplayLabel(vaultItem))}</span>
          </button>
          ${actionButton}
        </div>
      `;
    }).join('');
  }

  function renderFolderBreadcrumb() {
    if (!folderBreadcrumbEl) {
      return;
    }

    if (!vaultStatus || vaultStatus.locked) {
      folderBreadcrumbEl.hidden = true;
      folderBreadcrumbEl.innerHTML = '';
      return;
    }

    const chain = folderAncestorChain(selectedFolderPath).slice(1);
    if (!chain.length) {
      folderBreadcrumbEl.hidden = true;
      folderBreadcrumbEl.innerHTML = '';
      return;
    }

    folderBreadcrumbEl.hidden = false;
    folderBreadcrumbEl.innerHTML = chain.map((node, index) => {
      const separator = index === 0 ? '' : '<span class="vault-modal-folder-crumb-separator">/</span>';
      const active = index === chain.length - 1;

      if (active) {
        return `${separator}<span class="vault-modal-folder-crumb is-active">${escapeHTML(node.name)}</span>`;
      }

      return `${separator}<button type="button" class="vault-modal-folder-crumb" data-action="select-folder" data-folder-path="${escapeHTML(node.path)}">${escapeHTML(node.name)}</button>`;
    }).join('');
  }

  function renderSelectedRecordDetail() {
    if (!selectedRecord) {
      return '<div class="vault-modal-preview-detail-empty">Select a file in this encrypted folder to inspect its metadata and reveal its protected payload.</div>';
    }

    const record = selectedRecord;
    const attachments = entryAttachmentsFromPayload(record.payload);
    const tags = Array.isArray(record.tags) && record.tags.length > 0
      ? record.tags.map((tag) => `<span class="vault-modal-chip">${escapeHTML(tag)}</span>`).join('')
      : '<span class="vault-modal-chip is-muted">No tags</span>';
    const revealLabel = payloadRevealed ? 'Hide Payload' : 'Reveal Payload';
    const payloadClass = `vault-modal-payload-preview${payloadRevealed ? '' : ' is-concealed'}`;
    const attachmentsHTML = attachments.length
      ? `
        <div class="vault-modal-attachments-wrap">
          <div class="vault-modal-payload-label">Encrypted attachments</div>
          ${payloadRevealed
            ? `<div class="vault-modal-attachment-grid">${attachments.map(renderRecordAttachmentCard).join('')}</div>`
            : '<div class="vault-modal-preview-empty-inline">Reveal the payload to inspect or download attached files.</div>'}
        </div>
      `
      : '';

    return `
      <div class="vault-modal-detail">
        <div class="vault-modal-detail-header">
          <div>
            <div class="vault-modal-detail-title">${escapeHTML(record.label || 'Untitled entry')}</div>
            <div class="vault-modal-detail-meta">${escapeHTML([recordTypeLabel(record.type), record.workspace_id].filter(Boolean).join(' • ') || 'Vault file')}</div>
          </div>
          <div class="vault-modal-detail-actions">
            <button type="button" class="modern-btn modern-btn-secondary" data-action="focus-editor">Focus Editor</button>
            <button type="button" class="modern-btn modern-btn-secondary" data-action="toggle-payload">${escapeHTML(revealLabel)}</button>
            <button type="button" class="modern-btn modern-btn-secondary vault-modal-danger-btn" data-action="delete-record" data-record-id="${escapeHTML(record.id)}">Delete</button>
          </div>
        </div>
        <div class="vault-modal-chip-row">${tags}</div>
        <div class="vault-modal-detail-grid">
          <div class="vault-modal-detail-item"><span>Type</span><strong>${escapeHTML(recordTypeLabel(record.type))}</strong></div>
          <div class="vault-modal-detail-item"><span>Workspace</span><strong>${escapeHTML(record.workspace_id || 'Global')}</strong></div>
          <div class="vault-modal-detail-item"><span>Source</span><strong>${escapeHTML(record.source || 'Not set')}</strong></div>
          <div class="vault-modal-detail-item"><span>Retention</span><strong>${escapeHTML(record.retention_policy || 'Not set')}</strong></div>
          <div class="vault-modal-detail-item"><span>Attachments</span><strong>${String(attachments.length)}</strong></div>
          <div class="vault-modal-detail-item vault-modal-detail-item-wide"><span>Updated</span><strong>${escapeHTML(prettyDate(record.updated_at || record.created_at))}</strong></div>
        </div>
        ${attachmentsHTML}
        <div class="vault-modal-payload-wrap">
          <div class="vault-modal-payload-label">Protected payload</div>
          <pre class="${payloadClass}">${escapeHTML(prettyPayload(payloadForPreview(record.payload)))}</pre>
        </div>
      </div>
    `;
  }

  function renderExplorerPreview() {
    if (!explorerPreviewEl || !recordsSummaryEl) {
      return;
    }

    if (!vaultStatus) {
      recordsSummaryEl.textContent = 'Waiting for vault status...';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Checking vault availability.</div>';
      return;
    }

    if (!vaultStatus.available) {
      recordsSummaryEl.textContent = 'No vault selected';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Create a vault to start saving encrypted files and browsing them in the folder explorer.</div>';
      return;
    }

    if (vaultStatus.locked) {
      recordsSummaryEl.textContent = 'Vault is locked';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Unlock the vault to browse files inside the encrypted folder.</div>';
      return;
    }

    const folder = selectedFolder();
    if (!folder) {
      recordsSummaryEl.textContent = 'No folder selected';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Select a folder to continue.</div>';
      return;
    }

    const folderRecords = recordsForFolder(folder);
    const visibleRecords = filteredRecords(folderRecords);

    if (selectedRecord && !folderRecords.some((record) => record.id === selectedRecord.id)) {
      clearRecordForm({ preserveStatus: true, refreshExplorer: false });
    }

    recordsSummaryEl.textContent = `${visibleRecords.length} of ${folderRecords.length} ${folderRecords.length === 1 ? 'file' : 'files'} in ${folder.name}`;

    const filesHTML = visibleRecords.length > 0
      ? visibleRecords.map((record) => {
        const selectedClass = selectedRecord?.id === record.id ? ' is-selected' : '';
        const subtitle = [recordTypeLabel(record.type), record.workspace_id || 'Global'].join(' • ');
        const tags = Array.isArray(record.tags) ? record.tags.length : 0;

        return `
          <button type="button" class="vault-modal-preview-file${selectedClass}" data-action="select-record" data-record-id="${escapeHTML(record.id)}">
            <span class="vault-modal-preview-file-icon">${fileTypeIcon(record.type)}</span>
            <span class="vault-modal-preview-file-main">
              <span class="vault-modal-preview-file-name">${escapeHTML(record.label || 'Untitled entry')}</span>
              <span class="vault-modal-preview-file-meta">${escapeHTML(subtitle)}</span>
            </span>
            <span class="vault-modal-preview-file-side">
              <span class="vault-modal-preview-file-updated">${escapeHTML(prettyDate(record.updated_at || record.created_at))}</span>
              <span class="vault-modal-chip">${String(tags)} ${tags === 1 ? 'tag' : 'tags'}</span>
            </span>
          </button>
        `;
      }).join('')
      : '<div class="vault-modal-preview-empty-inline">No files match this folder and search combination yet.</div>';

    explorerPreviewEl.innerHTML = `
      <div class="vault-modal-preview-header">
        <div class="vault-modal-preview-title">${escapeHTML(folder.name)}</div>
        <div class="vault-modal-preview-subtitle">${escapeHTML(folderPathLabel(folder))}</div>
      </div>
      <div class="vault-modal-preview-stats">
        <span class="vault-modal-chip">${String(folderRecords.length)} ${folderRecords.length === 1 ? 'file' : 'files'}</span>
        <span class="vault-modal-chip is-muted">${escapeHTML(folder.description || 'Encrypted folder view')}</span>
      </div>
      <div class="vault-modal-preview-file-list">${filesHTML}</div>
      ${renderSelectedRecordDetail()}
    `;
  }

  function renderExplorer() {
    renderFolderVaultTabs();
    renderFolderTree();
    renderFolderBreadcrumb();
    renderExplorerPreview();
  }

  function selectFolder(folderPath) {
    if (!folderPath || !folderIndex.has(folderPath)) {
      return;
    }

    selectedFolderPath = folderPath;
    renderExplorer();
  }

  function toggleFolderNode(folderPath) {
    if (!folderPath || !folderIndex.has(folderPath) || folderPath === ROOT_FOLDER_PATH) {
      return;
    }

    if (expandedFolderPaths.has(folderPath)) {
      expandedFolderPaths.delete(folderPath);
    } else {
      expandedFolderPaths.add(folderPath);
    }

    renderFolderTree();
  }

  function renderGrantsList(items) {
    grants = Array.isArray(items) ? items : [];

    if (!grants.length) {
      grantsListEl.innerHTML = '<div class="vault-page-empty">No persistent grants configured.</div>';
      return;
    }

    grantsListEl.innerHTML = grants.map((grant) => `
      <div class="vault-page-grant">
        <div class="vault-page-grant-row">
          <div>
            <div class="vault-page-grant-title">${escapeHTML(grant.actor_type)}:${escapeHTML(grant.actor_id)}</div>
            <span class="vault-page-grant-meta">${escapeHTML(grant.workspace_id)} • ${escapeHTML(grant.capability)} • ${escapeHTML(grant.record_type || '*')}</span>
          </div>
          <button type="button" class="btn btn-sm btn-outline-danger vault-page-danger-btn" data-grant-id="${escapeHTML(grant.id)}">Revoke</button>
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
      records = [];
      recordIndex = new Map();
      rebuildFolderTree();
      renderRecordsList([]);
      renderExplorer();
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/records'));
    records = Array.isArray(data.records) ? data.records : [];
    recordIndex = new Map(records.map((record) => [record.id, record]));
    rebuildFolderTree();
    renderRecordsList(records);
    renderExplorer();
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
    const selectedRecordID = selectedRecord?.id || '';

    try {
      await loadVaults();
      const status = await loadVaultStatus();
      if (!status.available) {
        renderVaultSpaces();
        renderGrantsList([]);
        renderRecordsList([]);
        rebuildFolderTree();
        renderExplorer();
        return;
      }
      await loadVaultGrants();
      if (!status.locked) {
        await loadVaultRecords();
        if (selectedRecordID && recordIndex.has(selectedRecordID)) {
          await selectRecord(selectedRecordID, { keepFolder: true });
        } else {
          clearRecordForm({ preserveStatus: true, refreshList: true, refreshExplorer: true });
        }
      } else {
        renderVaultSpaces();
      }
    } catch (error) {
      console.error('Failed to refresh vault:', error);
      showInlineAlert(error.message || 'Failed to refresh vault.', 'error');
    }
  }

  async function selectRecord(recordID, options = {}) {
    if (!recordID) {
      clearRecordForm({ preserveStatus: true });
      return;
    }

    try {
      const record = await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(recordID)}`));
      const keepFolder = Boolean(options.keepFolder);
      if (!keepFolder) {
        const activeFolder = selectedFolder();
        const folderHasRecord = activeFolder && Array.isArray(activeFolder.recordIDs) && activeFolder.recordIDs.includes(record.id);
        if (!folderHasRecord) {
          selectedFolderPath = ROOT_FOLDER_PATH;
        }
      }
      applyRecordToForm(record);
      renderExplorer();
    } catch (error) {
      console.error('Failed to load vault record:', error);
      showInlineAlert(error.message || 'Failed to load vault record.', 'error');
    }
  }

  function focusEditor() {
    window.requestAnimationFrame(() => {
      entryLabelInput?.focus();
      entryLabelInput?.select();
    });
  }

  async function toggleVaultAccessFromExplorer(vaultID) {
    if (!vaultID) {
      return;
    }

    if (vaultID !== currentVaultID()) {
      switchVault(vaultID);
      return;
    }

    if (vaultStatus?.locked) {
      await unlockVault();
      return;
    }

    await lockVault();
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
      source: entrySourceInput.value.trim(),
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

    if (!String(unlockPasswordInput?.value || '').trim()) {
      showInlineAlert('Enter the selected vault password before unlocking.', 'warning');
      unlockPasswordInput?.focus();
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

  searchInput?.addEventListener('input', () => {
    renderExplorerPreview();
  });

  folderVaultTabsEl?.addEventListener('click', (event) => {
    const action = event.target.closest('[data-action]');
    if (!action) {
      return;
    }

    const vaultID = action.getAttribute('data-vault-id');
    if (action.getAttribute('data-action') === 'select-folder-vault-tab') {
      switchVault(vaultID);
      return;
    }

    if (action.getAttribute('data-action') === 'toggle-vault-access') {
      toggleVaultAccessFromExplorer(vaultID);
    }
  });

  folderTreeEl?.addEventListener('click', (event) => {
    const action = event.target.closest('[data-action]');
    if (!action) {
      return;
    }

    const folderPath = action.getAttribute('data-folder-path');
    if (action.getAttribute('data-action') === 'toggle-folder') {
      toggleFolderNode(folderPath);
      return;
    }

    if (action.getAttribute('data-action') === 'select-folder') {
      selectFolder(folderPath);
    }
  });

  folderBreadcrumbEl?.addEventListener('click', (event) => {
    const trigger = event.target.closest('[data-action="select-folder"]');
    if (!trigger) {
      return;
    }
    selectFolder(trigger.getAttribute('data-folder-path'));
  });

  explorerPreviewEl?.addEventListener('click', (event) => {
    const action = event.target.closest('[data-action]');
    if (!action) {
      return;
    }

    const actionName = action.getAttribute('data-action');
    if (actionName === 'select-record') {
      selectRecord(action.getAttribute('data-record-id'), { keepFolder: true });
      return;
    }

    if (actionName === 'toggle-payload') {
      togglePayloadReveal();
      return;
    }

    if (actionName === 'focus-editor') {
      focusEditor();
      return;
    }

    if (actionName === 'delete-record') {
      deleteRecord();
      return;
    }

    if (actionName === 'download-attachment') {
      const attachments = entryAttachmentsFromPayload(selectedRecord?.payload);
      const attachment = attachments.find((item) => item.id === action.getAttribute('data-attachment-id'));
      if (attachment) {
        downloadAttachment(attachment);
      }
    }
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

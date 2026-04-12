(function() {
  const DEFAULT_VAULT_ID = '';
  const VAULT_STORAGE_KEY = 'ori-selected-vault-id';
  const ROOT_FOLDER_PATH = 'vault';
  const DEFAULT_EXPANDED_FOLDERS = new Set([ROOT_FOLDER_PATH]);
  const MAX_ENTRY_ATTACHMENTS = 6;
  const MAX_ENTRY_ATTACHMENT_BYTES = 10 * 1024 * 1024;
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
  const EMAIL_PROVIDER_META = {
    gmail: 'Gmail / Google Workspace',
    microsoft: 'Microsoft 365 / Outlook',
    imap_smtp: 'Custom IMAP / SMTP'
  };
  const EMAIL_AUTH_TYPE_META = {
    oauth2: 'OAuth 2',
    password: 'Password',
    app_password: 'App Password'
  };
  const section = document.getElementById('private-vault');
  if (!section) {
    return;
  }

  const unlockOverlay = document.getElementById('vaultUnlockOverlay');
  const unlockDialogVaultName = document.getElementById('vaultUnlockDialogVaultName');
  const unlockDialogDescription = document.getElementById('vaultUnlockDialogDescription');
  const unlockPasswordInput = document.getElementById('vaultUnlockPassword');
  const unlockBtn = document.getElementById('vaultUnlockBtn');
  const unlockCancelBtn = document.getElementById('vaultUnlockCancelBtn');
  const toggleUnlockPasswordBtn = document.getElementById('toggleVaultUnlockPassword');
  const unlockPasswordHelp = document.getElementById('vaultUnlockPasswordHelp');
  const alertsEl = document.getElementById('vaultAlerts');
  const editVaultNameInput = document.getElementById('vaultEditVaultName');
  const editVaultDescriptionInput = document.getElementById('vaultEditVaultDescription');
  const renameVaultBtn = document.getElementById('vaultRenameVaultBtn');
  const deleteVaultBtn = document.getElementById('vaultDeleteVaultBtn');
  const openCreateDialogBtn = document.getElementById('vaultOpenCreateDialogBtn');
  const createOverlay = document.getElementById('vaultCreateOverlay');
  const createDialogDescription = document.getElementById('vaultCreateDialogDescription');
  const newVaultNameInput = document.getElementById('vaultNewVaultName');
  const newVaultDescriptionInput = document.getElementById('vaultNewVaultDescription');
  const newVaultPasswordInput = document.getElementById('vaultNewVaultPassword');
  const confirmVaultPasswordInput = document.getElementById('vaultConfirmVaultPassword');
  const createVaultSpaceBtn = document.getElementById('vaultCreateVaultSpaceBtn');
  const createCancelBtn = document.getElementById('vaultCreateCancelBtn');

  const entryTypeInput = document.getElementById('vaultEntryType');
  const entryWorkspaceInput = document.getElementById('vaultEntryWorkspaceId');
  const entryLabelInput = document.getElementById('vaultEntryLabel');
  const entryFolderPathInput = document.getElementById('vaultEntryFolderPath');
  const entryUseSelectedFolderBtn = document.getElementById('vaultEntryUseSelectedFolderBtn');
  const entryTagsInput = document.getElementById('vaultEntryTags');
  const entryRetentionInput = document.getElementById('vaultEntryRetention');
  const entryPayloadInput = document.getElementById('vaultEntryPayload');
  const saveEntryBtn = document.getElementById('vaultSaveEntryBtn');
  const resetEntryBtn = document.getElementById('vaultResetEntryBtn');
  const revealPayloadBtn = document.getElementById('vaultRevealPayloadBtn');
  const deleteEntryBtn = document.getElementById('vaultDeleteEntryBtn');
  const recordsListEl = document.getElementById('vaultRecordsList');

  const editorOverlay = document.getElementById('vaultEntryOverlay');
  const editorPanel = document.getElementById('vaultEditorPanel');
  const editorTitleEl = document.getElementById('vaultEditorTitle');
  const editorDescriptionEl = document.getElementById('vaultEditorDescription');
  const _editorForm = document.getElementById('vaultEditorForm');
  const editorCloseBtn = document.getElementById('vaultEditorCloseBtn');
  const explorerAddBtn = document.getElementById('vaultExplorerAddBtn');
  const entryAdvancedDetails = document.getElementById('vaultEntryAdvanced');
  const entryComposerLabel = document.getElementById('vaultEntryComposerLabel');
  const entryContentField = document.getElementById('vaultEntryContentField');
  const entryContentInput = document.getElementById('vaultEntryContent');
  const entryContentHelp = document.getElementById('vaultEntryContentHelp');
  const entryAttachBtn = document.getElementById('vaultEntryAttachBtn');
  const entryAttachmentsInput = document.getElementById('vaultEntryAttachmentsInput');
  const entryAttachmentsList = document.getElementById('vaultEntryAttachmentsList');
  const entryJsonModeInput = document.getElementById('vaultEntryJsonMode');
  const entryPayloadField = document.getElementById('vaultEntryPayloadField');

  const searchInput = document.getElementById('vaultPageSearchInput');
  const recordsSummaryEl = document.getElementById('vaultPageRecordsSummary');
  const folderVaultTabsEl = document.getElementById('vaultPageFolderVaultTabs');
  const folderBreadcrumbEl = document.getElementById('vaultPageFolderBreadcrumb');
  const folderTreeEl = document.getElementById('vaultPageFolderTree');
  const explorerPreviewEl = document.getElementById('vaultPageExplorerPreview');
  const explorerNewFolderBtn = document.getElementById('vaultExplorerNewFolderBtn');

  const grantWorkspaceInput = document.getElementById('vaultGrantWorkspaceId');
  const grantActorTypeInput = document.getElementById('vaultGrantActorType');
  const grantActorIDInput = document.getElementById('vaultGrantActorId');
  const grantCapabilityInput = document.getElementById('vaultGrantCapability');
  const grantRecordTypeInput = document.getElementById('vaultGrantRecordType');
  const createGrantBtn = document.getElementById('vaultCreateGrantBtn');
  const grantsListEl = document.getElementById('vaultGrantsList');

  const emailAccountsSummaryEl = document.getElementById('vaultEmailAccountsSummary');
  const emailAccountsListEl = document.getElementById('vaultEmailAccountsList');
  const emailAccountModeBadgeEl = document.getElementById('vaultEmailAccountModeBadge');
  const emailAccountLabelInput = document.getElementById('vaultEmailAccountLabel');
  const emailAccountAddressInput = document.getElementById('vaultEmailAccountAddress');
  const emailAccountProviderInput = document.getElementById('vaultEmailAccountProvider');
  const emailAccountAuthTypeInput = document.getElementById('vaultEmailAccountAuthType');
  const emailAccountDisplayNameInput = document.getElementById('vaultEmailAccountDisplayName');
  const emailAccountUsernameInput = document.getElementById('vaultEmailAccountUsername');
  const emailAccountWorkspaceInput = document.getElementById('vaultEmailAccountWorkspaceId');
  const emailAccountTagsInput = document.getElementById('vaultEmailAccountTags');
  const emailAccountSourceInput = document.getElementById('vaultEmailAccountSource');
  const emailAccountRetentionInput = document.getElementById('vaultEmailAccountRetention');
  const emailAccountImapFields = document.getElementById('vaultEmailAccountImapFields');
  const emailAccountImapHostInput = document.getElementById('vaultEmailAccountImapHost');
  const emailAccountImapPortInput = document.getElementById('vaultEmailAccountImapPort');
  const emailAccountSmtpHostInput = document.getElementById('vaultEmailAccountSmtpHost');
  const emailAccountSmtpPortInput = document.getElementById('vaultEmailAccountSmtpPort');
  const emailAccountConnectPanelEl = document.getElementById('vaultEmailAccountConnectPanel');
  const emailAccountConnectBtn = document.getElementById('vaultConnectEmailAccountBtn');
  const emailAccountOauthHelpEl = document.getElementById('vaultEmailOauthHelp');
  const emailAccountOauthProviderStatusEl = document.getElementById('vaultEmailOauthProviderStatus');
  const emailAccountCredentialHelpEl = document.getElementById('vaultEmailAccountCredentialHelp');
  const emailAccountOauthAdvancedEl = document.getElementById('vaultEmailAccountOauthAdvanced');
  const emailAccountOauthFields = document.getElementById('vaultEmailAccountOauthFields');
  const emailAccountPasswordFields = document.getElementById('vaultEmailAccountPasswordFields');
  const emailAccountRefreshTokenInput = document.getElementById('vaultEmailAccountRefreshToken');
  const emailAccountAccessTokenInput = document.getElementById('vaultEmailAccountAccessToken');
  const emailAccountClientIdInput = document.getElementById('vaultEmailAccountClientId');
  const emailAccountClientSecretInput = document.getElementById('vaultEmailAccountClientSecret');
  const emailAccountTokenEndpointInput = document.getElementById('vaultEmailAccountTokenEndpoint');
  const emailAccountPasswordInput = document.getElementById('vaultEmailAccountPassword');
  const emailAccountCredentialStatusEl = document.getElementById('vaultEmailAccountCredentialStatus');
  const saveEmailAccountBtn = document.getElementById('vaultSaveEmailAccountBtn');
  const resetEmailAccountBtn = document.getElementById('vaultResetEmailAccountBtn');
  const deleteEmailAccountBtn = document.getElementById('vaultDeleteEmailAccountBtn');

  const openExportDialogBtn = document.getElementById('vaultOpenExportDialogBtn');
  const exportOverlay = document.getElementById('vaultExportOverlay');
  const exportDialogVaultName = document.getElementById('vaultExportDialogVaultName');
  const exportDialogDescription = document.getElementById('vaultExportDialogDescription');
  const exportWorkspaceInput = document.getElementById('vaultExportWorkspaceId');
  const exportConfirmInput = document.getElementById('vaultExportConfirm');
  const exportPasswordInput = document.getElementById('vaultExportPassword');
  const exportBtn = document.getElementById('vaultExportBtn');
  const exportCancelBtn = document.getElementById('vaultExportCancelBtn');
  const openImportDialogBtn = document.getElementById('vaultOpenImportDialogBtn');
  const importOverlay = document.getElementById('vaultImportOverlay');
  const importDialogDescription = document.getElementById('vaultImportDialogDescription');
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
  const importCancelBtn = document.getElementById('vaultImportCancelBtn');

  function mountPageDialogOverlays() {
    const overlays = [
      unlockOverlay,
      createOverlay,
      exportOverlay,
      importOverlay,
      editorOverlay
    ];

    overlays.forEach((overlay) => {
      if (overlay && overlay.parentElement !== document.body) {
        document.body.appendChild(overlay);
      }
    });
  }

  mountPageDialogOverlays();

  let vaultStatus = null;
  let vaults = [];
  let selectedVaultID = DEFAULT_VAULT_ID;
  let records = [];
  let folders = [];
  let grants = [];
  let emailAccounts = [];
  let emailOAuthProviders = {};
  let emailOAuthProviderLoadError = '';
  let selectedRecord = null;
  let selectedEmailAccount = null;
  let emailOAuthPopup = null;
  let emailOAuthPopupPollID = 0;
  let emailOAuthPending = false;
  let payloadRevealed = false;
  let recordIndex = new Map();
  let folderIndex = new Map();
  let expandedFolderPaths = new Set(DEFAULT_EXPANDED_FOLDERS);
  let selectedFolderPath = ROOT_FOLDER_PATH;
  let folderComposerOpen = false;
  let unlockDialogOpen = false;
  let createDialogOpen = false;
  let exportDialogOpen = false;
  let importDialogOpen = false;
  let editorDialogOpen = false;
  let entryAttachments = [];
  let entryAttachmentSnapshot = [];
  let draggedRecordID = '';
  let draggedRecordElement = null;
  let activeDropFolderButton = null;

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

  function syncDialogBodyState() {
    document.body.classList.toggle('vault-page-dialog-open', Boolean(
      unlockDialogOpen
      || createDialogOpen
      || exportDialogOpen
      || importDialogOpen
      || editorDialogOpen
    ));
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
    const isFormData = options.body instanceof window.FormData;
    const config = {
      method: options.method || 'GET',
      headers: {
        ...(options.headers || {})
      }
    };

    if (options.body !== undefined && config.method !== 'GET') {
      if (isFormData) {
        config.body = options.body;
      } else {
        config.headers['Content-Type'] = 'application/json';
        config.body = JSON.stringify(options.body);
      }
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

    const file = item.file instanceof File ? item.file : null;
    const name = String(item.name || file?.name || '').trim();
    const contentBase64 = String(item.content_base64 || item.base64_data || '').trim();
    const downloadURL = String(item.download_url || item.downloadURL || '').trim();
    if (!name || (!file && !contentBase64 && !downloadURL)) {
      return null;
    }

    const mimeType = String(item.mime_type || item.mimeType || file?.type || 'application/octet-stream').trim() || 'application/octet-stream';
    const sizeBytes = Number(item.size_bytes ?? item.size ?? file?.size ?? base64ByteLength(contentBase64));

    return {
      id: String(item.id || generateAttachmentID()),
      name,
      mime_type: mimeType,
      size_bytes: Number.isFinite(sizeBytes) && sizeBytes >= 0 ? sizeBytes : 0,
      kind: String(item.kind || '').trim() || attachmentKindForMimeType(mimeType),
      content_base64: contentBase64 || undefined,
      download_url: downloadURL || undefined,
      file: file || undefined
    };
  }

  function cloneEntryAttachment(item) {
    const normalized = normalizeEntryAttachment(item);
    return normalized ? { ...normalized } : null;
  }

  function cloneEntryAttachments(items) {
    return (Array.isArray(items) ? items : []).map(cloneEntryAttachment).filter(Boolean);
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
    const downloadURL = String(attachment?.download_url || '').trim();
    if (downloadURL) {
      return downloadURL;
    }

    const mimeType = String(attachment?.mime_type || 'application/octet-stream').trim() || 'application/octet-stream';
    const contentBase64 = String(attachment?.content_base64 || '').trim();
    return contentBase64 ? `data:${mimeType};base64,${contentBase64}` : '';
  }

  function base64ByteLength(value) {
    const normalized = String(value || '').trim().replace(/\s+/g, '');
    if (!normalized) {
      return 0;
    }

    const paddingMatch = normalized.match(/=+$/);
    const padding = paddingMatch ? paddingMatch[0].length : 0;
    return Math.max(0, Math.floor((normalized.length * 3) / 4) - padding);
  }

  function attachmentBlob(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      throw new Error('That attachment could not be prepared.');
    }

    if (normalized.file instanceof File) {
      return normalized.file;
    }

    const contentBase64 = String(normalized.content_base64 || '').trim();
    if (!contentBase64) {
      throw new Error('That attachment is missing file content.');
    }

    const binary = window.atob(contentBase64);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }

    return new Blob([bytes], {
      type: normalized.mime_type || 'application/octet-stream'
    });
  }

  function hasStoredAttachment(attachment) {
    return Boolean(String(attachment?.download_url || '').trim());
  }

  function readFileAsBase64(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();

      reader.onload = () => {
        const result = String(reader.result || '');
        const commaIndex = result.indexOf(',');
        resolve(commaIndex >= 0 ? result.slice(commaIndex + 1) : result);
      };

      reader.onerror = () => {
        reject(new Error(`Failed to read file "${String(file?.name || 'attachment')}".`));
      };

      reader.readAsDataURL(file);
    });
  }

  function totalAttachmentBytes(attachments) {
    return (Array.isArray(attachments) ? attachments : []).reduce((total, attachment) => {
      return total + Number(attachment?.size_bytes || 0);
    }, 0);
  }

  function renderEntryAttachments() {
    if (!entryAttachmentsList) {
      return;
    }

    if (!entryAttachments.length) {
      entryAttachmentsList.innerHTML = '<div class="vault-modal-attachments-empty">No files or images attached yet.</div>';
      return;
    }

    entryAttachmentsList.innerHTML = entryAttachments.map((attachment) => {
      return `
        <div class="vault-modal-attachment-item">
          <div class="vault-modal-attachment-item-main">
            <span class="vault-modal-attachment-icon">${attachmentIcon(attachment.kind)}</span>
            <div class="vault-modal-attachment-copy">
              <div class="vault-modal-attachment-name">${escapeHTML(attachment.name)}</div>
              <div class="vault-modal-attachment-meta">${escapeHTML([attachment.kind === 'image' ? 'Image' : 'File', formatBytes(attachment.size_bytes)].join(' • '))}</div>
            </div>
          </div>
          <button type="button" class="modern-btn modern-btn-secondary vault-modal-attachment-remove" data-action="remove-entry-attachment" data-attachment-id="${escapeHTML(attachment.id)}">Remove</button>
        </div>
      `;
    }).join('');
  }

  async function addEntryAttachments(fileList) {
    const files = Array.from(fileList || []);
    if (!files.length) {
      return;
    }

    const nextAttachments = entryAttachments.slice();
    const rejected = [];

    if (nextAttachments.length >= MAX_ENTRY_ATTACHMENTS) {
      showInlineAlert(`You can attach up to ${String(MAX_ENTRY_ATTACHMENTS)} files per vault entry.`, 'warning');
      return;
    }

    for (const file of files) {
      if (nextAttachments.length >= MAX_ENTRY_ATTACHMENTS) {
        rejected.push(file.name);
        continue;
      }

      const projectedBytes = totalAttachmentBytes(nextAttachments) + Number(file.size || 0);
      if (projectedBytes > MAX_ENTRY_ATTACHMENT_BYTES) {
        rejected.push(file.name);
        continue;
      }

      nextAttachments.push({
        id: generateAttachmentID(),
        name: String(file.name || 'attachment'),
        mime_type: String(file.type || 'application/octet-stream'),
        size_bytes: Number(file.size || 0),
        kind: attachmentKindForMimeType(file.type),
        file
      });
    }

    entryAttachments = nextAttachments;
    renderEntryAttachments();

    if (entryAttachmentsInput) {
      entryAttachmentsInput.value = '';
    }

    if (rejected.length) {
      showInlineAlert(`Some attachments were skipped. Entries support up to ${String(MAX_ENTRY_ATTACHMENTS)} files and ${formatBytes(MAX_ENTRY_ATTACHMENT_BYTES)} total.`, 'warning');
      return;
    }

    showInlineAlert('');
  }

  function removeEntryAttachment(attachmentID) {
    entryAttachments = entryAttachments.filter((item) => item.id !== attachmentID);
    renderEntryAttachments();
  }

  function entrySimplePayload(type, content) {
    const normalized = normalizeRecordType(type);
    const value = String(content || '').trim();

    if (!value) {
      return {};
    }

    if (normalized === 'email_snippet') {
      return { body: value };
    }

    if (normalized === 'secret') {
      return { value: value };
    }

    return { note: value };
  }

  function entryContentFromPayload(type, payload) {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return '';
    }

    const normalized = normalizeRecordType(type);

    if (normalized === 'email_snippet') {
      return String(payload.body || '').trim();
    }

    if (normalized === 'secret') {
      return String(payload.value || '').trim();
    }

    return String(payload.note || '').trim();
  }

  function defaultPayloadValue(type) {
    const normalized = normalizeRecordType(type);

    if (normalized === 'email_snippet') {
      return '{\n  "subject": "",\n  "body": ""\n}';
    }

    if (normalized === 'secret') {
      return '{\n  "value": ""\n}';
    }

    return '{\n  "note": ""\n}';
  }

  function isDefaultPayloadTemplate(raw) {
    const normalized = String(raw || '').trim();
    return !normalized || ['personal_note', 'email_snippet', 'secret'].some((type) => {
      return normalized === defaultPayloadValue(type);
    });
  }

  function entryLabelPlaceholder(type) {
    const normalized = normalizeRecordType(type);

    if (normalized === 'email_snippet') {
      return 'Tax reply, landlord update, important follow-up';
    }

    if (normalized === 'secret') {
      return 'Bank login, Wi-Fi password, recovery code';
    }

    return 'Passport note, tax reminder, emergency contact';
  }

  function entryContentPlaceholder(type) {
    const normalized = normalizeRecordType(type);

    if (normalized === 'email_snippet') {
      return 'Paste the email text or the key excerpt you want to keep.';
    }

    if (normalized === 'secret') {
      return 'Paste the password, code, or secret text you want to store.';
    }

    return 'Write the private details you want to keep encrypted.';
  }

  function simplePayloadKey(type) {
    const normalized = normalizeRecordType(type);

    if (normalized === 'email_snippet') {
      return 'body';
    }

    if (normalized === 'secret') {
      return 'value';
    }

    return 'note';
  }

  function canUseSimpleEntryComposer(type, payload) {
    if (payload === undefined || payload === null) {
      return true;
    }

    if (typeof payload !== 'object' || Array.isArray(payload)) {
      return false;
    }

    const keys = Object.keys(payload);
    if (!keys.length) {
      return true;
    }

    const key = simplePayloadKey(type);
    const dataKeys = keys.filter((item) => item !== 'attachments');

    if (!dataKeys.length) {
      return true;
    }

    return dataKeys.length === 1 && dataKeys[0] === key && typeof payload[key] === 'string';
  }

  function syncEntryComposerPresentation() {
    const normalizedType = normalizeRecordType(entryTypeInput?.value || 'personal_note');
    const useJSON = Boolean(entryJsonModeInput?.checked);

    if (entryComposerLabel) {
      entryComposerLabel.textContent = useJSON ? 'Structured payload' : 'Private content';
      entryComposerLabel.setAttribute('for', useJSON ? 'vaultEntryPayload' : 'vaultEntryContent');
    }

    if (entryLabelInput) {
      entryLabelInput.placeholder = entryLabelPlaceholder(normalizedType);
    }

    if (entryContentInput) {
      entryContentInput.placeholder = entryContentPlaceholder(normalizedType);
    }

    if (entryContentHelp) {
      entryContentHelp.textContent = useJSON
        ? 'Use JSON only if you want named fields or nested data.'
        : 'Write normal text here. Ori will turn it into an encrypted record for you.';
    }

    if (entryContentField) {
      entryContentField.hidden = useJSON;
    }

    if (entryPayloadField) {
      entryPayloadField.hidden = !useJSON;
    }

    if (useJSON && entryPayloadInput) {
      const rawPayload = String(entryPayloadInput.value || '').trim();
      if (isDefaultPayloadTemplate(rawPayload)) {
        const content = String(entryContentInput?.value || '').trim();
        entryPayloadInput.value = content
          ? prettyPayload(entrySimplePayload(normalizedType, content))
          : defaultPayloadValue(normalizedType);
      }
    } else if (!useJSON && entryContentInput) {
      const currentContent = String(entryContentInput.value || '').trim();
      const rawPayload = String(entryPayloadInput?.value || '').trim();
      if (!currentContent && rawPayload) {
        try {
          const payload = JSON.parse(rawPayload);
          const extracted = entryContentFromPayload(normalizedType, payload);
          if (extracted) {
            entryContentInput.value = extracted;
          }
        } catch (error) {
          // Leave the plain-text composer blank when the payload is intentionally custom.
        }
      }
    }
  }

  function downloadAttachment(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      showInlineAlert('That attachment could not be opened.', 'warning');
      return;
    }

    const link = document.createElement('a');
    const objectURL = normalized.file instanceof File
      ? window.URL.createObjectURL(normalized.file)
      : '';
    link.href = objectURL || attachmentDataURL(normalized);
    link.download = normalized.name || 'vault-attachment';
    link.style.display = 'none';
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    if (objectURL) {
      window.setTimeout(() => {
        window.URL.revokeObjectURL(objectURL);
      }, 0);
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

  function normalizeRecordType(type) {
    return String(type || '').trim().toLowerCase() || 'personal_note';
  }

  function recordTypeLabel(type) {
    const normalized = normalizeRecordType(type);
    return TYPE_META[normalized]?.label || normalized.replaceAll('_', ' ');
  }

  function normalizeEmailProvider(provider) {
    return String(provider || '').trim().toLowerCase().replaceAll('-', '_');
  }

  function emailProviderLabel(provider) {
    const normalized = normalizeEmailProvider(provider);
    return EMAIL_PROVIDER_META[normalized] || normalized.replaceAll('_', ' ');
  }

  function normalizeEmailAuthType(authType) {
    return String(authType || '').trim().toLowerCase();
  }

  function emailAuthTypeLabel(authType) {
    const normalized = normalizeEmailAuthType(authType);
    return EMAIL_AUTH_TYPE_META[normalized] || normalized.replaceAll('_', ' ');
  }

  function isNativeOAuthProvider(provider) {
    const normalized = normalizeEmailProvider(provider);
    return normalized === 'gmail' || normalized === 'microsoft';
  }

  function oauthProviderButtonLabel(provider) {
    const normalized = normalizeEmailProvider(provider);
    if (normalized === 'gmail') {
      return 'Google';
    }
    if (normalized === 'microsoft') {
      return 'Microsoft';
    }
    return emailProviderLabel(normalized);
  }

  function currentEmailOAuthProviderStatus(provider = emailAccountProviderInput?.value) {
    const normalized = normalizeEmailProvider(provider);
    const fallbackReason = emailOAuthProviderLoadError
      ? emailOAuthProviderLoadError
      : isNativeOAuthProvider(normalized)
        ? `Checking ${oauthProviderButtonLabel(normalized)} OAuth availability...`
        : 'This provider uses manual credentials or advanced token import.';

    return emailOAuthProviders[normalized] || {
      provider: normalized,
      label: oauthProviderButtonLabel(normalized),
      connect_supported: isNativeOAuthProvider(normalized),
      enabled: false,
      reason: fallbackReason
    };
  }

  function currentEmailOAuthConnectMode() {
    return normalizeEmailAuthType(emailAccountAuthTypeInput?.value) === 'oauth2'
      && isNativeOAuthProvider(emailAccountProviderInput?.value);
  }

  function emailAccountHasStoredConnection(account) {
    const state = currentEmailAccountSecretState(account);
    const authType = normalizeEmailAuthType(account?.auth_type);
    if (authType === 'oauth2') {
      return Boolean(state.has_refresh_token || state.has_access_token);
    }
    if (authType === 'password' || authType === 'app_password') {
      return Boolean(state.has_password);
    }
    return false;
  }

  function emailAccountConnectionChip(account) {
    const authType = normalizeEmailAuthType(account?.auth_type);
    const isConnected = emailAccountHasStoredConnection(account);

    if (authType === 'oauth2') {
      return isConnected
        ? '<span class="vault-page-email-chip is-success">Connected</span>'
        : '<span class="vault-page-email-chip is-warning">Needs connection</span>';
    }

    if (authType === 'app_password') {
      return isConnected
        ? '<span class="vault-page-email-chip is-success">App password stored</span>'
        : '<span class="vault-page-email-chip is-warning">Needs app password</span>';
    }

    if (authType === 'password') {
      return isConnected
        ? '<span class="vault-page-email-chip is-success">Password stored</span>'
        : '<span class="vault-page-email-chip is-warning">Needs password</span>';
    }

    return '';
  }

  function parseOptionalPort(value) {
    const parsed = Number.parseInt(String(value || '').trim(), 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
  }

  function recordTypeFolderName(type) {
    const normalized = normalizeRecordType(type);
    return TYPE_META[normalized]?.folderName || recordTypeLabel(normalized);
  }

  function normalizeFolderPathInput(value) {
    const rawSegments = String(value || '')
      .replaceAll('\\', '/')
      .split('/');
    const segments = rawSegments
      .map((segment) => segment.trim())
      .filter(Boolean);

    if (segments.some((segment) => segment === '.' || segment === '..')) {
      throw new Error('Folder paths cannot contain "." or "..".');
    }

    return segments.join('/');
  }

  function folderNodePath(folderPath) {
    const normalized = normalizeFolderPathInput(folderPath);
    return normalized ? `folder:${normalized}` : ROOT_FOLDER_PATH;
  }

  function folderPathFromNodePath(nodePath) {
    const normalized = String(nodePath || '').trim();
    if (!normalized || normalized === ROOT_FOLDER_PATH) {
      return '';
    }
    return normalized.startsWith('folder:') ? normalized.slice('folder:'.length) : normalized;
  }

  function folderPathSegments(folderPath) {
    const normalized = normalizeFolderPathInput(folderPath);
    return normalized ? normalized.split('/') : [];
  }

  function folderPathAncestors(folderPath) {
    const segments = folderPathSegments(folderPath);
    return segments.map((_, index) => segments.slice(0, index + 1).join('/'));
  }

  function folderNameFromPath(folderPath) {
    const segments = folderPathSegments(folderPath);
    return segments[segments.length - 1] || vaultDisplayLabel(currentVault()) || 'Vault Root';
  }

  function selectedFolderRelativePath() {
    return folderPathFromNodePath(selectedFolderPath);
  }

  function joinFolderPath(basePath, nextPath) {
    const normalizedBase = normalizeFolderPathInput(basePath);
    const normalizedNext = normalizeFolderPathInput(nextPath);

    if (!normalizedBase) {
      return normalizedNext;
    }
    if (!normalizedNext) {
      return normalizedBase;
    }
    return `${normalizedBase}/${normalizedNext}`;
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
    const useJSON = Boolean(entryJsonModeInput?.checked);
    if (!useJSON) {
      return entrySimplePayload(entryTypeInput?.value, entryContentInput?.value);
    }

    const raw = String(entryPayloadInput?.value || '').trim();
    if (!raw) {
      return {};
    }

    try {
      return JSON.parse(raw);
    } catch (error) {
      throw new Error('Payload must be valid JSON.');
    }
  }

  function showInlineAlert(message, type = 'info') {
    if (!message) {
      if (alertsEl) {
        alertsEl.innerHTML = '';
      }
      window.clearTimeout(showInlineAlert.timeoutId);
      return;
    }

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

  function syncUnlockDialog() {
    if (!unlockOverlay) {
      return;
    }

    const selectedVault = currentVault();
    const vaultLabel = vaultDisplayLabel(selectedVault);
    const canShow = Boolean(unlockDialogOpen && vaultStatus?.available && vaultStatus?.locked);

    if (unlockDialogVaultName) {
      unlockDialogVaultName.textContent = vaultLabel;
    }

    if (unlockDialogDescription) {
      unlockDialogDescription.textContent = `Enter the password for ${vaultLabel} to load it inside Ori.`;
    }

    if (!canShow) {
      unlockDialogOpen = false;
    }

    unlockOverlay.hidden = !canShow;

    if (!canShow && unlockPasswordInput) {
      unlockPasswordInput.value = '';
      unlockPasswordInput.type = 'password';
    }

    syncDialogBodyState();
  }

  function closeUnlockDialog(options = {}) {
    unlockDialogOpen = false;
    syncUnlockDialog();

    if (options.restoreFocus !== false) {
      window.requestAnimationFrame(() => {
        const focusTarget = folderVaultTabsEl?.querySelector('.vault-modal-folder-vault-tab-icon')
          || folderVaultTabsEl?.querySelector('.vault-modal-folder-vault-tab.is-active')
          || explorerAddBtn;
        focusTarget?.focus();
      });
    }
  }

  function openUnlockDialog() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before unlocking storage access.', 'warning');
      return;
    }

    if (!vaultStatus?.available) {
      showInlineAlert('Select a vault before unlocking storage access.', 'warning');
      return;
    }

    if (!vaultStatus.locked) {
      return;
    }

    closeCreateDialog({ restoreFocus: false });
    closeExportDialog({ restoreFocus: false });
    closeImportDialog({ restoreFocus: false });
    closeVaultEditor({ restoreFocus: false });
    unlockDialogOpen = true;
    syncUnlockDialog();
    window.requestAnimationFrame(() => {
      unlockPasswordInput?.focus();
    });
  }

  function resetCreateForm() {
    if (newVaultNameInput) newVaultNameInput.value = '';
    if (newVaultDescriptionInput) newVaultDescriptionInput.value = '';
    if (newVaultPasswordInput) newVaultPasswordInput.value = '';
    if (confirmVaultPasswordInput) confirmVaultPasswordInput.value = '';
  }

  function syncCreateDialog() {
    if (!createOverlay) {
      return;
    }

    const hasVaults = vaults.length > 0;
    const canShow = Boolean(createDialogOpen);

    if (createDialogDescription) {
      createDialogDescription.textContent = hasVaults
        ? 'Create a separately encrypted vault with its own password.'
        : 'Start with one encrypted vault. Each vault has its own password and protected record set.';
    }

    createOverlay.hidden = !canShow;

    if (!canShow && createVaultSpaceBtn && !createVaultSpaceBtn.dataset.originalLabel) {
      resetCreateForm();
    }

    syncDialogBodyState();
  }

  function closeCreateDialog(options = {}) {
    createDialogOpen = false;
    syncCreateDialog();

    if (options.restoreFocus !== false) {
      window.requestAnimationFrame(() => {
        openCreateDialogBtn?.focus();
      });
    }
  }

  function openCreateDialog() {
    closeUnlockDialog({ restoreFocus: false });
    closeExportDialog({ restoreFocus: false });
    closeImportDialog({ restoreFocus: false });
    closeVaultEditor({ restoreFocus: false });
    createDialogOpen = true;
    syncCreateDialog();
    window.requestAnimationFrame(() => {
      newVaultNameInput?.focus();
    });
  }

  function resetExportForm() {
    if (exportWorkspaceInput) exportWorkspaceInput.value = '';
    if (exportPasswordInput) exportPasswordInput.value = '';
    if (exportConfirmInput) exportConfirmInput.checked = false;
  }

  function syncExportDialog() {
    if (!exportOverlay) {
      return;
    }

    const selectedVault = currentVault();
    const vaultLabel = vaultDisplayLabel(selectedVault);
    const canShow = Boolean(exportDialogOpen && currentVaultID());

    if (exportDialogVaultName) {
      exportDialogVaultName.textContent = vaultLabel;
    }

    if (exportDialogDescription) {
      exportDialogDescription.textContent = `Create a password-protected export bundle for ${vaultLabel} so it can be moved or archived safely.`;
    }

    exportOverlay.hidden = !canShow;

    if (!canShow && exportBtn && !exportBtn.dataset.originalLabel) {
      resetExportForm();
    }

    syncDialogBodyState();
  }

  function closeExportDialog(options = {}) {
    exportDialogOpen = false;
    syncExportDialog();

    if (options.restoreFocus !== false) {
      window.requestAnimationFrame(() => {
        openExportDialogBtn?.focus();
      });
    }
  }

  function openExportDialog() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before exporting.', 'warning');
      return;
    }

    if (!vaultStatus?.available || vaultStatus.locked) {
      showInlineAlert('Unlock the selected vault before exporting it.', 'warning');
      return;
    }

    closeUnlockDialog({ restoreFocus: false });
    closeCreateDialog({ restoreFocus: false });
    closeImportDialog({ restoreFocus: false });
    closeVaultEditor({ restoreFocus: false });
    exportDialogOpen = true;
    syncExportDialog();
    window.requestAnimationFrame(() => {
      exportPasswordInput?.focus();
    });
  }

  function resetImportForm() {
    if (importFileInput) importFileInput.value = '';
    if (importFileName) importFileName.textContent = 'No import file selected yet.';
    if (importPasswordInput) importPasswordInput.value = '';
    if (importModeInput) importModeInput.value = 'new';
    if (importVaultNameInput) importVaultNameInput.value = '';
    if (importVaultDescriptionInput) importVaultDescriptionInput.value = '';
    if (importVaultPasswordInput) importVaultPasswordInput.value = '';
    if (importConfirmVaultPasswordInput) importConfirmVaultPasswordInput.value = '';
    if (importRestoreGrantsInput) importRestoreGrantsInput.checked = true;
  }

  function syncImportDialog() {
    if (!importOverlay) {
      return;
    }

    syncImportControls();

    const currentVaultName = vaultDisplayLabel(currentVault());
    const mode = importModeInput?.value === 'current' && canImportIntoCurrentVault() ? 'current' : 'new';
    const canShow = Boolean(importDialogOpen);

    if (importDialogDescription) {
      importDialogDescription.textContent = mode === 'current'
        ? `Import the encrypted bundle into ${currentVaultName}. The selected vault stays unlocked while Ori writes the restored records.`
        : 'Import the encrypted bundle into a freshly created vault with its own Ori password.';
    }

    importOverlay.hidden = !canShow;

    if (!canShow && importBtn && !importBtn.dataset.originalLabel) {
      resetImportForm();
      syncImportControls();
    }

    syncDialogBodyState();
  }

  function closeImportDialog(options = {}) {
    importDialogOpen = false;
    syncImportDialog();

    if (options.restoreFocus !== false) {
      window.requestAnimationFrame(() => {
        openImportDialogBtn?.focus();
      });
    }
  }

  function openImportDialog() {
    closeUnlockDialog({ restoreFocus: false });
    closeCreateDialog({ restoreFocus: false });
    closeExportDialog({ restoreFocus: false });
    closeVaultEditor({ restoreFocus: false });
    importDialogOpen = true;
    syncImportDialog();
    window.requestAnimationFrame(() => {
      importChooseFileBtn?.focus();
    });
  }

  function syncEditorDialog() {
    if (!editorOverlay || !editorPanel) {
      return;
    }

    const vaultLabel = vaultDisplayLabel(currentVault());
    const editing = Boolean(selectedRecord?.id);
    const recordLabel = String(selectedRecord?.label || '').trim();
    const canShow = Boolean(editorDialogOpen && vaultStatus?.available && !vaultStatus?.locked && currentVaultID());

    if (editorTitleEl) {
      editorTitleEl.textContent = editing ? 'Edit Vault Item' : 'New Vault Item';
    }

    if (editorDescriptionEl) {
      editorDescriptionEl.textContent = editing
        ? `Update ${recordLabel || 'this vault item'} in ${vaultLabel}.`
        : 'Add a private item, choose the folder it belongs in, and only open advanced details if you need them.';
    }

    if (saveEntryBtn && !saveEntryBtn.dataset.originalLabel) {
      saveEntryBtn.textContent = editing ? 'Update Entry' : 'Save Entry';
    }

    if (resetEntryBtn) {
      resetEntryBtn.textContent = editing ? 'Reset Changes' : 'Clear';
    }

    if (!canShow) {
      editorDialogOpen = false;
    }

    editorOverlay.hidden = !canShow;
    editorPanel.hidden = !canShow;
    if (canShow) {
      syncEntryComposerPresentation();
      renderEntryAttachments();
      if (entryLabelInput) {
        entryLabelInput.setAttribute('aria-label', `Label for ${vaultLabel}`);
      }
    }
    syncDialogBodyState();
  }

  function closeVaultEditor(options = {}) {
    editorDialogOpen = false;
    syncEditorDialog();

    if (options.restoreFocus !== false) {
      window.requestAnimationFrame(() => {
        const focusTarget = recordsListEl?.querySelector('.vault-page-record.is-selected') || explorerAddBtn;
        focusTarget?.focus();
      });
    }
  }

  function syncPageDialogs() {
    syncUnlockDialog();
    syncCreateDialog();
    syncExportDialog();
    syncImportDialog();
    syncEditorDialog();
  }

  function renderVaultSpaces() {
    if (!vaults.length) {
      if (editVaultNameInput) editVaultNameInput.value = '';
      if (editVaultDescriptionInput) editVaultDescriptionInput.value = '';
      if (renameVaultBtn) renameVaultBtn.disabled = true;
      if (deleteVaultBtn) deleteVaultBtn.disabled = true;
      syncImportControls();
      syncPageDialogs();
      return;
    }

    const selectedVault = currentVault();
    if (!selectedVault) {
      if (editVaultNameInput) editVaultNameInput.value = '';
      if (editVaultDescriptionInput) editVaultDescriptionInput.value = '';
      if (renameVaultBtn) renameVaultBtn.disabled = true;
      if (deleteVaultBtn) deleteVaultBtn.disabled = true;
      syncImportControls();
      syncPageDialogs();
      return;
    }

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
    syncPageDialogs();
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

    closeUnlockDialog({ restoreFocus: false });
    closeCreateDialog({ restoreFocus: false });
    closeExportDialog({ restoreFocus: false });
    closeImportDialog({ restoreFocus: false });
    folderComposerOpen = false;
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
      folderComposerOpen = false;
      closeCreateDialog({ restoreFocus: false });
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
      closeUnlockDialog({ restoreFocus: false });
      closeCreateDialog({ restoreFocus: false });
      closeExportDialog({ restoreFocus: false });
      closeImportDialog({ restoreFocus: false });
      folderComposerOpen = false;
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

    if (saveEntryBtn) {
      saveEntryBtn.disabled = disableVaultEditing;
    }
    if (resetEntryBtn) {
      resetEntryBtn.disabled = false;
    }
    exportBtn.disabled = disableVaultEditing;
    if (unlockBtn) {
      unlockBtn.disabled = !vaultStatus || !vaultStatus.available || !vaultStatus.locked;
    }
    if (revealPayloadBtn) {
      revealPayloadBtn.disabled = disableVaultEditing || !selectedRecord;
    }
    if (deleteEntryBtn) {
      deleteEntryBtn.disabled = disableVaultEditing || !selectedRecord;
    }
    if (searchInput) {
      searchInput.disabled = !vaultStatus?.available || Boolean(locked);
    }
    if (openExportDialogBtn) {
      openExportDialogBtn.disabled = !vaultStatus?.available || Boolean(locked);
    }
    if (explorerAddBtn) {
      explorerAddBtn.disabled = disableVaultEditing;
    }
    if (explorerNewFolderBtn) {
      explorerNewFolderBtn.disabled = disableVaultEditing;
    }
    setEmailAccountFormDisabled(disableVaultEditing);
    renderFolderVaultTabs();

    if (disableVaultEditing) {
      folderComposerOpen = false;
      records = [];
      folders = [];
      recordIndex = new Map();
      emailAccounts = [];
      rebuildFolderTree();
      recordsListEl.innerHTML = vaultStatus?.available
        ? '<div class="vault-page-empty">Unlock the vault to browse saved items.</div>'
        : '<div class="vault-page-empty">Create a vault to begin storing encrypted items.</div>';
      renderEmailAccountsList([]);
      clearEmailAccountForm({ refreshList: false });
      renderEmailAccountsSummary();
      clearRecordForm({ preserveStatus: true, refreshList: false, refreshExplorer: false, hide: true });
      renderExplorer();
    }
  }

  function renderStatus(state) {
    vaultStatus = state || null;
    if (!vaultStatus) {
      return;
    }

    if (!vaultStatus.available) {
      if (unlockPasswordHelp) {
        unlockPasswordHelp.textContent = 'Per-vault passwords are required for new vaults. Legacy vaults may still unlock through secure system storage or the older fallback passphrase flow.';
      }
      setInteractiveState(true);
      syncImportControls();
      syncPageDialogs();
      return;
    }

    const locked = Boolean(vaultStatus.locked);
    const passwordProtected = Boolean(vaultStatus.password_protected);

    if (unlockPasswordHelp) {
      unlockPasswordHelp.textContent = passwordProtected
        ? 'Enter the password for the selected vault to unlock it.'
        : 'This legacy vault may still unlock through secure system storage or the older fallback passphrase flow.';
    }

    setInteractiveState(locked);
    syncImportControls();
    syncPageDialogs();
  }

  function showVaultEditor() {
    closeUnlockDialog({ restoreFocus: false });
    closeCreateDialog({ restoreFocus: false });
    closeExportDialog({ restoreFocus: false });
    closeImportDialog({ restoreFocus: false });
    editorDialogOpen = true;
    syncEditorDialog();

    if (editorPanel) {
      editorPanel.classList.remove('animate__fadeOut');
      editorPanel.classList.add('animate__fadeIn');
    }

    if (!selectedRecord && entryLabelInput) {
      entryLabelInput.focus();
    }
  }

  function hideVaultEditor(force = false) {
    if (force) {
      closeVaultEditor({ restoreFocus: false });
    }
  }

  function clearRecordForm(options = {}) {
    const preserveStatus = Boolean(options.preserveStatus);
    const refreshList = options.refreshList !== false;
    const refreshExplorer = options.refreshExplorer !== false;
    const hide = Boolean(options.hide);

    selectedRecord = null;
    payloadRevealed = false;

    if (hide) {
      hideVaultEditor(true);
    }
    entryTypeInput.value = 'personal_note';
    entryWorkspaceInput.value = '';
    entryLabelInput.value = '';
    if (entryFolderPathInput) {
      entryFolderPathInput.value = selectedFolderRelativePath();
    }
    if (entryContentInput) {
      entryContentInput.value = '';
    }
    entryAttachments = [];
    entryAttachmentSnapshot = [];
    if (entryJsonModeInput) {
      entryJsonModeInput.checked = false;
    }
    entryTagsInput.value = '';
    entryRetentionInput.value = '';
    entryPayloadInput.value = defaultPayloadValue(entryTypeInput.value);
    if (entryAttachmentsInput) {
      entryAttachmentsInput.value = '';
    }
    if (entryAdvancedDetails) {
      entryAdvancedDetails.open = false;
    }
    renderEntryAttachments();
    syncEntryComposerPresentation();
    syncEditorDialog();

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
    showVaultEditor();
    const normalizedType = normalizeRecordType(record.type);
    const payload = record.payload ?? {};
    const payloadCore = payloadWithoutAttachments(payload) || {};
    const useJSON = !canUseSimpleEntryComposer(normalizedType, payload);
    const payloadValue = prettyPayload(payloadCore);

    entryTypeInput.value = normalizedType;
    entryWorkspaceInput.value = record.workspace_id || '';
    entryLabelInput.value = record.label || '';
    if (entryFolderPathInput) {
      entryFolderPathInput.value = record.folder_path || '';
    }
    if (entryContentInput) {
      entryContentInput.value = entryContentFromPayload(normalizedType, payload);
    }
    if (entryJsonModeInput) {
      entryJsonModeInput.checked = useJSON;
    }
    entryTagsInput.value = Array.isArray(record.tags) ? record.tags.join(', ') : '';
    entryRetentionInput.value = record.retention_policy || '';
    entryAttachments = entryAttachmentsFromPayload(payload);
    entryAttachmentSnapshot = cloneEntryAttachments(entryAttachments);
    entryPayloadInput.value = payloadValue === '{}' ? defaultPayloadValue(normalizedType) : payloadValue;
    if (entryAdvancedDetails) {
      entryAdvancedDetails.open = Boolean(
        record.workspace_id ||
        (Array.isArray(record.tags) && record.tags.length) ||
        record.retention_policy
      );
    }

    renderEntryAttachments();
    syncEntryComposerPresentation();
    setInteractiveState(Boolean(vaultStatus?.locked));
    syncEditorDialog();
    renderRecordsList(records);
    renderExplorerPreview();
    window.requestAnimationFrame(() => {
      entryLabelInput?.focus();
      entryLabelInput?.select();
    });
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
      recordsListEl.innerHTML = '<div class="vault-page-empty">No vault items saved yet.</div>';
      return;
    }

    recordsListEl.innerHTML = records.map((record) => {
      const isSelected = selectedRecord?.id === record.id ? ' is-selected' : '';
      const subtitleParts = [recordTypeLabel(record.type)];
      if (record.folder_path) {
        subtitleParts.push(record.folder_path);
      }
      if (record.workspace_id) {
        subtitleParts.push(record.workspace_id);
      }
      const subtitle = escapeHTML(subtitleParts.join(' • '));
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
      folderPath: String(options?.folderPath || ''),
      name,
      parentPath: parentPath || '',
      recordIDs: Array.isArray(recordIDs) ? recordIDs.slice() : [],
      directRecordIDs: Array.isArray(options?.directRecordIDs) ? options.directRecordIDs.slice() : [],
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
    const activeVaultItem = currentVault();
    const rootLabel = vaultDisplayLabel(activeVaultItem) || 'Vault Root';
    const folderPaths = new Set();
    const itemsByID = new Map(items.map((record) => [record.id, record]));

    addFolderNode(index, createFolderNode(ROOT_FOLDER_PATH, rootLabel, '', [], {
      folderPath: '',
      description: 'Everything in the active vault, including nested folders.',
      meta: 'Vault root'
    }));

    folders.forEach((folder) => {
      folderPathAncestors(folder.path).forEach((path) => folderPaths.add(path));
    });
    items.forEach((record) => {
      folderPathAncestors(record.folder_path || '').forEach((path) => folderPaths.add(path));
    });

    Array.from(folderPaths)
      .sort((left, right) => {
        const leftDepth = folderPathSegments(left).length;
        const rightDepth = folderPathSegments(right).length;
        if (leftDepth !== rightDepth) {
          return leftDepth - rightDepth;
        }
        return left.localeCompare(right, undefined, { sensitivity: 'base' });
      })
      .forEach((folderPath) => {
        const parentSegments = folderPathSegments(folderPath);
        parentSegments.pop();
        const parentPath = parentSegments.join('/');
        addFolderNode(index, createFolderNode(
          folderNodePath(folderPath),
          folderNameFromPath(folderPath),
          folderNodePath(parentPath),
          [],
          {
            folderPath,
            description: folderPath,
            meta: 'Encrypted folder'
          }
        ));
      });

    items.forEach((record) => {
      [ROOT_FOLDER_PATH, ...folderPathAncestors(record.folder_path || '').map(folderNodePath)].forEach((nodePath) => {
        const node = index.get(nodePath);
        if (node) {
          node.recordIDs.push(record.id);
        }
      });

      const directNode = index.get(folderNodePath(record.folder_path || ''));
      if (directNode) {
        directNode.directRecordIDs.push(record.id);
      }
    });

    index.forEach((node) => {
      node.children.sort((left, right) => {
        const leftNode = index.get(left);
        const rightNode = index.get(right);
        return String(leftNode?.name || '').localeCompare(String(rightNode?.name || ''), undefined, { sensitivity: 'base' });
      });
      node.directRecordIDs.sort((leftID, rightID) => {
        const leftRecord = itemsByID.get(leftID);
        const rightRecord = itemsByID.get(rightID);
        return String(leftRecord?.label || leftRecord?.id || '').localeCompare(String(rightRecord?.label || rightRecord?.id || ''), undefined, { sensitivity: 'base' });
      });
    });

    return index;
  }

  function rebuildFolderTree() {
    folderIndex = buildFolderTree(records);
    expandedFolderPaths.add(ROOT_FOLDER_PATH);

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

  function directRecordsForFolder(folder) {
    if (!folder || !Array.isArray(folder.directRecordIDs)) {
      return [];
    }

    return folder.directRecordIDs.map(recordByID).filter(Boolean);
  }

  function filteredRecords(items) {
    const query = String(searchInput?.value || '').trim().toLowerCase();
    if (!query) {
      return items.slice();
    }

    return items.filter((record) => {
      const haystack = [
        record.label,
        record.folder_path,
        record.type,
        record.workspace_id,
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

  function isRootFolderPath(nodePath) {
    return String(nodePath || '') === ROOT_FOLDER_PATH;
  }

  function openFolderComposer() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before creating a folder.', 'warning');
      return;
    }
    if (!vaultStatus?.available || vaultStatus?.locked) {
      showInlineAlert('Unlock the selected vault before creating a folder.', 'warning');
      return;
    }

    folderComposerOpen = true;
    renderFolderTree();
    window.requestAnimationFrame(() => {
      folderTreeEl?.querySelector('[data-folder-create-input]')?.focus();
    });
  }

  function closeFolderComposer() {
    folderComposerOpen = false;
    renderFolderTree();
  }

  async function createFolderFromComposer() {
    const input = folderTreeEl?.querySelector('[data-folder-create-input]');
    const relativePath = String(input?.value || '').trim();
    const basePath = selectedFolderRelativePath();

    let nextPath = '';
    try {
      nextPath = joinFolderPath(basePath, relativePath);
    } catch (error) {
      showInlineAlert(error.message || 'Folder path is invalid.', 'warning');
      return;
    }

    if (!nextPath) {
      showInlineAlert('Folder name is required.', 'warning');
      return;
    }

    try {
      await apiRequest(vaultURL('/api/vault/folders'), {
        method: 'POST',
        body: {
          vault_id: currentVaultID(),
          path: nextPath
        }
      });
      folderComposerOpen = false;
      selectedFolderPath = folderNodePath(nextPath);
      notify('Folder created.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to create folder:', error);
      showInlineAlert(error.message || 'Failed to create folder.', 'error');
    }
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
    const childCount = Array.isArray(node.children) ? node.children.length : 0;
    const itemLabel = `${count} ${count === 1 ? 'item' : 'items'}`;
    if (childCount < 1) {
      return itemLabel;
    }
    return `${itemLabel} • ${childCount} ${childCount === 1 ? 'folder' : 'folders'}`;
  }

  function clearFolderDropTarget() {
    if (!activeDropFolderButton) {
      return;
    }

    activeDropFolderButton.classList.remove('is-drop-target');
    activeDropFolderButton = null;
  }

  function setFolderDropTarget(button) {
    if (activeDropFolderButton === button) {
      return;
    }

    clearFolderDropTarget();
    if (!button) {
      return;
    }

    button.classList.add('is-drop-target');
    activeDropFolderButton = button;
  }

  function dragRecordButtonFromTarget(target) {
    if (!(target instanceof Element)) {
      return null;
    }

    return target.closest('[data-drag-record="true"][data-record-id]');
  }

  function folderDropButtonFromTarget(target) {
    if (!(target instanceof Element)) {
      return null;
    }

    return target.closest('.vault-modal-folder-main[data-action="select-folder"]')
      || target.closest('.vault-modal-folder-node')?.querySelector('.vault-modal-folder-main[data-action="select-folder"]')
      || null;
  }

  function beginRecordDrag(button, event) {
    if (!(button instanceof HTMLElement)) {
      return;
    }

    const recordID = String(button.getAttribute('data-record-id') || '').trim();
    if (!recordID) {
      return;
    }

    if (draggedRecordElement && draggedRecordElement !== button) {
      draggedRecordElement.classList.remove('is-dragging');
    }

    draggedRecordID = recordID;
    draggedRecordElement = button;
    draggedRecordElement.classList.add('is-dragging');

    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', recordID);
    }
  }

  function clearRecordDrag() {
    if (draggedRecordElement) {
      draggedRecordElement.classList.remove('is-dragging');
    }

    draggedRecordID = '';
    draggedRecordElement = null;
    clearFolderDropTarget();
  }

  async function moveRecordToFolder(recordID, folderPath) {
    const record = recordByID(recordID);
    if (!record) {
      return;
    }

    const currentFolderPath = normalizeFolderPathInput(record.folder_path || '');
    const nextFolderPath = normalizeFolderPathInput(folderPath);
    if (currentFolderPath === nextFolderPath) {
      return;
    }

    try {
      await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(recordID)}`), {
        method: 'PATCH',
        body: {
          folder_path: nextFolderPath
        }
      });

      [ROOT_FOLDER_PATH, ...folderPathAncestors(nextFolderPath).map(folderNodePath)].forEach((path) => {
        expandedFolderPaths.add(path);
      });
      selectedFolderPath = folderNodePath(nextFolderPath);
      selectedRecord = record;
      payloadRevealed = false;
      notify('Item moved.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to move vault item:', error);
      showInlineAlert(error.message || 'Failed to move item.', 'error');
    }
  }

  function renderFolderTreeRecord(recordID, folderPath, depth) {
    const record = recordByID(recordID);
    if (!record) {
      return '';
    }

    const isSelected = selectedRecord?.id === record.id;
    const metaParts = [recordTypeLabel(record.type)];
    if (record.workspace_id) {
      metaParts.push(record.workspace_id);
    }

    return `
      <div class="vault-modal-tree-record${isSelected ? ' is-selected' : ''}">
        <div class="vault-modal-folder-row vault-modal-tree-record-row" style="--vault-tree-depth:${String(depth)};">
          <span class="vault-modal-folder-spacer"></span>
          <button type="button" class="vault-modal-tree-record-main" draggable="true" data-action="select-tree-record" data-drag-record="true" data-folder-path="${escapeHTML(folderPath)}" data-record-id="${escapeHTML(record.id)}">
            <span class="vault-modal-tree-record-icon">${fileTypeIcon(record.type)}</span>
            <span class="vault-modal-tree-record-label">${escapeHTML(record.label || 'Untitled entry')}</span>
            <span class="vault-modal-tree-record-meta">${escapeHTML(metaParts.join(' • '))}</span>
          </button>
        </div>
      </div>
    `;
  }

  function renderFolderTreeNode(nodePath, depth, forceExpanded) {
    const node = folderIndex.get(nodePath);
    if (!node) {
      return '';
    }

    const hasFolderChildren = Array.isArray(node.children) && node.children.length > 0;
    const hasRecordChildren = Array.isArray(node.directRecordIDs) && node.directRecordIDs.length > 0;
    const hasChildren = hasFolderChildren || hasRecordChildren;
    const canToggle = hasChildren;
    const isExpanded = hasChildren && (forceExpanded || expandedFolderPaths.has(node.path));
    const isSelected = selectedFolderPath === node.path;

    const toggle = canToggle
      ? `<button type="button" class="vault-modal-folder-toggle${isExpanded ? ' is-expanded' : ''}" data-action="toggle-folder" data-folder-path="${escapeHTML(node.path)}" aria-label="${isExpanded ? 'Collapse folder' : 'Expand folder'}">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8.59,16.59L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.59Z"/></svg>
        </button>`
      : '<span class="vault-modal-folder-spacer"></span>';

    const childFoldersHTML = hasChildren && isExpanded
      ? node.children.map((childPath) => renderFolderTreeNode(childPath, depth + 1, false)).join('')
      : '';
    const recordChildrenHTML = isExpanded
      ? directRecordsForFolder(node).map((record) => renderFolderTreeRecord(record.id, node.path, depth + 1)).join('')
      : '';
    const childrenHTML = childFoldersHTML || recordChildrenHTML
      ? `<div class="vault-modal-folder-children">${childFoldersHTML}${recordChildrenHTML}</div>`
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
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">No vault exists yet. Create your first encrypted vault to start saving private items.</div>';
      return;
    }

    if (vaultStatus.locked) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">Use the lock icon on the selected vault tab to unlock the vault and browse saved items.</div>';
      return;
    }

    const root = folderIndex.get(ROOT_FOLDER_PATH);
    if (!root) {
      folderTreeEl.innerHTML = '<div class="vault-modal-empty">No saved items available yet.</div>';
      return;
    }

    const destinationLabel = isRootFolderPath(selectedFolderPath) ? 'vault root' : selectedFolder()?.name || 'selected folder';

    folderTreeEl.innerHTML = `
      <div class="vault-modal-folder-tree-scroll">
        ${folderComposerOpen
          ? `
            <div class="vault-modal-folder-create-card">
              <div class="vault-modal-folder-create-copy">Create a folder inside ${escapeHTML(destinationLabel)}.</div>
              <div class="vault-modal-folder-create-row">
                <input type="text" class="form-control vault-modal-input" data-folder-create-input placeholder="Folder name or nested path">
                <button type="button" class="modern-btn modern-btn-primary modern-btn-sm" data-action="save-folder-composer">Save</button>
                <button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-action="cancel-folder-composer">Cancel</button>
              </div>
            </div>
          `
          : ''}
        ${renderFolderTreeNode(ROOT_FOLDER_PATH, 0, true)}
      </div>
    `;
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
      return '<div class="vault-modal-preview-detail-empty">Select an item to inspect its metadata and reveal its protected payload.</div>';
    }

    const record = selectedRecord;
    const attachments = entryAttachmentsFromPayload(record.payload);
    const tags = Array.isArray(record.tags) && record.tags.length > 0
      ? record.tags.map((tag) => `<span class="vault-modal-chip">${escapeHTML(tag)}</span>`).join('')
      : '<span class="vault-modal-chip is-muted">No tags</span>';
    const revealLabel = payloadRevealed ? 'Hide Payload' : 'Reveal Payload';
    const payloadClass = `vault-modal-payload-preview${payloadRevealed ? '' : ' is-concealed'}`;
    const payloadToggleButton = `<button type="button" class="modern-btn modern-btn-secondary" data-action="toggle-payload">${escapeHTML(revealLabel)}</button>`;
    const attachmentsHTML = attachments.length
      ? `
        <div class="vault-modal-attachments-wrap">
          <div class="vault-modal-inline-head">
            <div class="vault-modal-inline-head-copy">
              <div class="vault-modal-payload-label">Encrypted attachments</div>
              ${payloadRevealed
                ? ''
                : '<div class="vault-modal-preview-empty-inline">Reveal the payload to inspect or download attached files.</div>'}
            </div>
            ${payloadToggleButton}
          </div>
          ${payloadRevealed
            ? `<div class="vault-modal-attachment-grid">${attachments.map(renderRecordAttachmentCard).join('')}</div>`
            : ''}
        </div>
      `
      : '';

    return `
      <div class="vault-modal-detail">
        <div class="vault-modal-detail-header">
          <div>
            <div class="vault-modal-detail-title">${escapeHTML(record.label || 'Untitled entry')}</div>
            <div class="vault-modal-detail-meta">${escapeHTML([recordTypeLabel(record.type), record.workspace_id].filter(Boolean).join(' • ') || 'Vault item')}</div>
          </div>
          <div class="vault-modal-detail-actions">
            <button type="button" class="modern-btn modern-btn-secondary" data-action="focus-editor">Edit Item</button>
            <button type="button" class="modern-btn modern-btn-secondary vault-modal-danger-btn" data-action="delete-record" data-record-id="${escapeHTML(record.id)}">Delete Item</button>
          </div>
        </div>
        <div class="vault-modal-chip-row">${tags}</div>
        <div class="vault-modal-detail-grid">
          <div class="vault-modal-detail-item"><span>Type</span><strong>${escapeHTML(recordTypeLabel(record.type))}</strong></div>
          <div class="vault-modal-detail-item"><span>Folder</span><strong>${escapeHTML(record.folder_path || 'Vault root')}</strong></div>
          <div class="vault-modal-detail-item"><span>Workspace</span><strong>${escapeHTML(record.workspace_id || 'Global')}</strong></div>
          <div class="vault-modal-detail-item"><span>Retention</span><strong>${escapeHTML(record.retention_policy || 'Not set')}</strong></div>
          <div class="vault-modal-detail-item"><span>Attachments</span><strong>${String(attachments.length)}</strong></div>
          <div class="vault-modal-detail-item vault-modal-detail-item-wide"><span>Updated</span><strong>${escapeHTML(prettyDate(record.updated_at || record.created_at))}</strong></div>
        </div>
        ${attachmentsHTML}
        <div class="vault-modal-payload-wrap">
          ${attachments.length
            ? '<div class="vault-modal-payload-label">Protected payload</div>'
            : `
              <div class="vault-modal-inline-head">
                <div class="vault-modal-inline-head-copy">
                  <div class="vault-modal-payload-label">Protected payload</div>
                </div>
                ${payloadToggleButton}
              </div>
            `}
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
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Create a vault to start saving encrypted items in your private library.</div>';
      return;
    }

    if (vaultStatus.locked) {
      recordsSummaryEl.textContent = 'Vault is locked';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Use the lock icon on the selected vault tab to unlock the vault and browse saved items.</div>';
      return;
    }

    const folder = selectedFolder();
    if (!folder) {
      recordsSummaryEl.textContent = 'No view selected';
      explorerPreviewEl.innerHTML = '<div class="vault-modal-empty">Select a folder to continue.</div>';
      return;
    }

    const folderRecords = recordsForFolder(folder);
    const visibleRecords = filteredRecords(folderRecords);

    if (selectedRecord && !folderRecords.some((record) => record.id === selectedRecord.id)) {
      clearRecordForm({ preserveStatus: true, refreshExplorer: false });
    }

    recordsSummaryEl.textContent = `${visibleRecords.length} of ${folderRecords.length} ${folderRecords.length === 1 ? 'item' : 'items'} in ${folder.name}`;

    const filesHTML = visibleRecords.length > 0
      ? visibleRecords.map((record) => {
        const selectedClass = selectedRecord?.id === record.id ? ' is-selected' : '';
        const subtitle = [recordTypeLabel(record.type), record.workspace_id || 'Global'].join(' • ');
        const tags = Array.isArray(record.tags) ? record.tags.length : 0;

        return `
          <button type="button" class="vault-modal-preview-file${selectedClass}" draggable="true" data-action="select-record" data-drag-record="true" data-record-id="${escapeHTML(record.id)}">
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
      : '<div class="vault-modal-preview-empty-inline">No items match this view and search combination yet.</div>';

    const previewSubtitle = folder.path === ROOT_FOLDER_PATH
      ? 'Vault root'
      : folderPathLabel(folder);

    explorerPreviewEl.innerHTML = `
      <div class="vault-modal-preview-header">
        <div class="vault-modal-preview-title">${escapeHTML(folder.name)}</div>
        <div class="vault-modal-preview-subtitle">${escapeHTML(previewSubtitle)}</div>
      </div>
      <div class="vault-modal-preview-stats">
        <span class="vault-modal-chip">${String(folderRecords.length)} ${folderRecords.length === 1 ? 'item' : 'items'}</span>
        <span class="vault-modal-chip is-muted">${escapeHTML(folder.description || 'Encrypted folder')}</span>
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

  function currentEmailAccountSecretState(account) {
    if (!account || typeof account !== 'object') {
      return {};
    }

    if (account.credentials_status && typeof account.credentials_status === 'object') {
      return account.credentials_status;
    }
    if (account.credentials && typeof account.credentials === 'object') {
      return account.credentials;
    }
    return {};
  }

  function emailAccountSecretsSummary(account) {
    const state = currentEmailAccountSecretState(account);
    const stored = [];
    const provider = normalizeEmailProvider(account?.provider || emailAccountProviderInput?.value);
    const connectMode = currentEmailOAuthConnectMode();

    if (state.has_refresh_token) stored.push('Refresh token');
    if (state.has_access_token) stored.push('Access token');
    if (state.has_password) stored.push(normalizeEmailAuthType(account?.auth_type) === 'app_password' ? 'App password' : 'Password');
    if (state.has_client_id) stored.push('Client ID');
    if (state.has_client_secret) stored.push('Client secret');

    if (stored.length === 0) {
      if (connectMode) {
        return `This account is not connected yet. Use Connect with ${oauthProviderButtonLabel(provider)} to continue.`;
      }
      return 'No credentials stored yet.';
    }

    if (normalizeEmailAuthType(account?.auth_type || emailAccountAuthTypeInput?.value) === 'oauth2') {
      return `Connected through OAuth. Stored in vault: ${stored.join(', ')}.`;
    }
    return `Stored in vault: ${stored.join(', ')}.`;
  }

  function renderEmailAccountCredentialState(account) {
    if (!emailAccountCredentialStatusEl) {
      return;
    }

    const message = emailAccountSecretsSummary(account);
    emailAccountCredentialStatusEl.textContent = message;
  }

  function renderEmailAccountActionButtons() {
    if (!saveEmailAccountBtn) {
      return;
    }

    const connectMode = currentEmailOAuthConnectMode();
    const editingExisting = Boolean(selectedEmailAccount?.id);

    saveEmailAccountBtn.classList.toggle('modern-btn-primary', !connectMode);
    saveEmailAccountBtn.classList.toggle('modern-btn-secondary', connectMode);

    if (connectMode) {
      saveEmailAccountBtn.textContent = editingExisting ? 'Save Metadata' : 'Save Manual Setup';
      return;
    }

    saveEmailAccountBtn.textContent = editingExisting ? 'Save Changes' : 'Save Email Account';
  }

  function clearEmailOAuthPopupState(options = {}) {
    if (emailOAuthPopupPollID) {
      window.clearInterval(emailOAuthPopupPollID);
      emailOAuthPopupPollID = 0;
    }

    if (options.close && emailOAuthPopup && !emailOAuthPopup.closed) {
      emailOAuthPopup.close();
    }

    emailOAuthPopup = null;
    emailOAuthPending = false;
    setButtonLoading(emailAccountConnectBtn, false);
  }

  function renderEmailOAuthConnectPanel() {
    const provider = normalizeEmailProvider(emailAccountProviderInput?.value);
    const authType = normalizeEmailAuthType(emailAccountAuthTypeInput?.value);
    const connectMode = authType === 'oauth2' && isNativeOAuthProvider(provider);
    const providerStatus = currentEmailOAuthProviderStatus(provider);
    const buttonProviderLabel = oauthProviderButtonLabel(provider);
    const accountMatchesProvider = normalizeEmailProvider(selectedEmailAccount?.provider) === provider;
    const hasConnectedAccount = accountMatchesProvider && emailAccountHasStoredConnection(selectedEmailAccount);

    if (emailAccountConnectPanelEl) {
      emailAccountConnectPanelEl.hidden = !connectMode;
    }
    if (emailAccountOauthAdvancedEl) {
      emailAccountOauthAdvancedEl.hidden = authType !== 'oauth2';
    }
    if (emailAccountPasswordFields) {
      emailAccountPasswordFields.hidden = authType === 'oauth2';
    }

    if (emailAccountCredentialHelpEl) {
      if (connectMode) {
        emailAccountCredentialHelpEl.textContent = hasConnectedAccount
          ? 'Ori keeps the OAuth tokens write-only. You can reconnect to rotate them, or save metadata changes without reconnecting.'
          : `Ori will save the OAuth tokens only after ${buttonProviderLabel} approves access.`;
      } else if (authType === 'oauth2') {
        emailAccountCredentialHelpEl.textContent = 'Paste existing OAuth tokens or custom client credentials only if you already manage this provider outside Ori.';
      } else {
        emailAccountCredentialHelpEl.textContent = 'Leave password fields blank when editing unless you want to replace the stored secret.';
      }
    }

    if (!connectMode) {
      renderEmailAccountActionButtons();
      return;
    }

    const readyToConnect = Boolean(
      providerStatus.enabled
        && currentVaultID()
        && vaultStatus?.available
        && !vaultStatus?.locked
    );

    if (emailAccountOauthHelpEl) {
      emailAccountOauthHelpEl.textContent = hasConnectedAccount
        ? `Reconnect through ${buttonProviderLabel} if you want Ori to replace the stored refresh token.`
        : `Use the ${buttonProviderLabel} sign-in window. Ori will create the account only after the provider returns a token.`;
    }

    if (emailAccountOauthProviderStatusEl) {
      let state = 'warning';
      let message = providerStatus.reason || 'OAuth is unavailable for this provider.';

      if (readyToConnect) {
        state = hasConnectedAccount ? 'connected' : 'ready';
        message = hasConnectedAccount
          ? 'Connected. You can reconnect any time to refresh mailbox access.'
          : 'Ready. No account record is saved until the provider approves access.';
      }

      emailAccountOauthProviderStatusEl.dataset.state = state;
      emailAccountOauthProviderStatusEl.textContent = message;
    }

    if (emailAccountConnectBtn) {
      emailAccountConnectBtn.textContent = `${hasConnectedAccount ? 'Reconnect with' : 'Connect with'} ${buttonProviderLabel}`;
      emailAccountConnectBtn.disabled = !readyToConnect;
    }

    renderEmailAccountActionButtons();
  }

  function renderEmailAccountsSummary() {
    if (!emailAccountsSummaryEl) {
      return;
    }

    if (!currentVaultID()) {
      emailAccountsSummaryEl.textContent = 'Select a vault to manage email accounts.';
      return;
    }

    if (!vaultStatus?.available) {
      emailAccountsSummaryEl.textContent = 'Create a vault before saving reusable email identities.';
      return;
    }

    if (vaultStatus.locked) {
      emailAccountsSummaryEl.textContent = 'Unlock the selected vault to review or edit email accounts.';
      return;
    }

    const vaultLabel = vaultDisplayLabel(currentVault());
    if (!emailAccounts.length) {
      emailAccountsSummaryEl.textContent = `No email accounts saved in ${vaultLabel} yet.`;
      return;
    }

    emailAccountsSummaryEl.textContent = `${emailAccounts.length} email account${emailAccounts.length === 1 ? '' : 's'} stored in ${vaultLabel}.`;
  }

  function setEmailAccountFormDisabled(disabled) {
    [
      emailAccountLabelInput,
      emailAccountAddressInput,
      emailAccountProviderInput,
      emailAccountAuthTypeInput,
      emailAccountDisplayNameInput,
      emailAccountUsernameInput,
      emailAccountWorkspaceInput,
      emailAccountTagsInput,
      emailAccountSourceInput,
      emailAccountRetentionInput,
      emailAccountImapHostInput,
      emailAccountImapPortInput,
      emailAccountSmtpHostInput,
      emailAccountSmtpPortInput,
      emailAccountRefreshTokenInput,
      emailAccountAccessTokenInput,
      emailAccountClientIdInput,
      emailAccountClientSecretInput,
      emailAccountTokenEndpointInput,
      emailAccountPasswordInput,
      emailAccountConnectBtn,
      saveEmailAccountBtn,
      deleteEmailAccountBtn
    ].forEach((element) => {
      if (element) {
        element.disabled = Boolean(disabled);
      }
    });

    if (resetEmailAccountBtn) {
      resetEmailAccountBtn.disabled = false;
    }
  }

  function resetEmailCredentialInputs() {
    if (emailAccountRefreshTokenInput) emailAccountRefreshTokenInput.value = '';
    if (emailAccountAccessTokenInput) emailAccountAccessTokenInput.value = '';
    if (emailAccountClientIdInput) emailAccountClientIdInput.value = '';
    if (emailAccountClientSecretInput) emailAccountClientSecretInput.value = '';
    if (emailAccountTokenEndpointInput) emailAccountTokenEndpointInput.value = '';
    if (emailAccountPasswordInput) emailAccountPasswordInput.value = '';
  }

  function syncEmailAccountProviderFields() {
    const provider = normalizeEmailProvider(emailAccountProviderInput?.value);
    if (emailAccountImapFields) {
      emailAccountImapFields.hidden = provider !== 'imap_smtp';
    }
    renderEmailOAuthConnectPanel();
  }

  function syncEmailAccountAuthFields() {
    const authType = normalizeEmailAuthType(emailAccountAuthTypeInput?.value);
    if (emailAccountOauthAdvancedEl) {
      emailAccountOauthAdvancedEl.hidden = authType !== 'oauth2';
    }
    if (emailAccountPasswordFields) {
      emailAccountPasswordFields.hidden = authType === 'oauth2';
    }
    renderEmailOAuthConnectPanel();
  }

  function renderEmailAccountsList(items = emailAccounts) {
    emailAccounts = Array.isArray(items) ? items : [];

    if (!emailAccountsListEl) {
      return;
    }

    if (!currentVaultID()) {
      emailAccountsListEl.innerHTML = '<div class="vault-page-empty">Select a vault to manage reusable email identities.</div>';
      return;
    }

    if (!vaultStatus?.available) {
      emailAccountsListEl.innerHTML = '<div class="vault-page-empty">Create a vault before adding reusable email identities.</div>';
      return;
    }

    if (vaultStatus.locked) {
      emailAccountsListEl.innerHTML = '<div class="vault-page-empty">Unlock the selected vault to review stored email accounts.</div>';
      return;
    }

    if (!emailAccounts.length) {
      emailAccountsListEl.innerHTML = '<div class="vault-page-empty">No email accounts stored in this vault yet.</div>';
      return;
    }

    emailAccountsListEl.innerHTML = emailAccounts.map((account) => {
      const isSelected = selectedEmailAccount?.id === account.id ? ' is-selected' : '';
      const subtitle = [account.email_address, emailProviderLabel(account.provider)].filter(Boolean).join(' • ');
      const chips = [
        emailAccountConnectionChip(account),
        `<span class="vault-page-email-chip">${escapeHTML(emailAuthTypeLabel(account.auth_type))}</span>`,
        account.workspace_id ? `<span class="vault-page-email-chip">Workspace: ${escapeHTML(account.workspace_id)}</span>` : '<span class="vault-page-email-chip">Global</span>'
      ].filter(Boolean).join('');

      return `
        <button type="button" class="vault-page-record vault-page-email-account${isSelected}" data-email-account-id="${escapeHTML(account.id)}">
          <div class="vault-page-record-row">
            <div class="vault-page-email-account-main">
              <div class="vault-page-record-title">${escapeHTML(account.label || account.email_address || 'Untitled account')}</div>
              <span class="vault-page-record-meta">${escapeHTML(subtitle)}</span>
              <div class="vault-page-email-chip-row">${chips}</div>
            </div>
            <span class="vault-page-record-updated">${escapeHTML(prettyDate(account.updated_at || account.created_at))}</span>
          </div>
        </button>
      `;
    }).join('');
  }

  function clearEmailAccountForm(options = {}) {
    selectedEmailAccount = null;

    if (emailAccountLabelInput) emailAccountLabelInput.value = '';
    if (emailAccountAddressInput) emailAccountAddressInput.value = '';
    if (emailAccountProviderInput) emailAccountProviderInput.value = 'gmail';
    if (emailAccountAuthTypeInput) emailAccountAuthTypeInput.value = 'oauth2';
    if (emailAccountDisplayNameInput) emailAccountDisplayNameInput.value = '';
    if (emailAccountUsernameInput) emailAccountUsernameInput.value = '';
    if (emailAccountWorkspaceInput) emailAccountWorkspaceInput.value = '';
    if (emailAccountTagsInput) emailAccountTagsInput.value = '';
    if (emailAccountSourceInput) emailAccountSourceInput.value = '';
    if (emailAccountRetentionInput) emailAccountRetentionInput.value = '';
    if (emailAccountImapHostInput) emailAccountImapHostInput.value = '';
    if (emailAccountImapPortInput) emailAccountImapPortInput.value = '';
    if (emailAccountSmtpHostInput) emailAccountSmtpHostInput.value = '';
    if (emailAccountSmtpPortInput) emailAccountSmtpPortInput.value = '';
    if (emailAccountOauthAdvancedEl) emailAccountOauthAdvancedEl.open = false;
    resetEmailCredentialInputs();
    syncEmailAccountProviderFields();
    syncEmailAccountAuthFields();

    if (emailAccountModeBadgeEl) {
      emailAccountModeBadgeEl.textContent = 'New account';
    }
    if (deleteEmailAccountBtn) {
      deleteEmailAccountBtn.classList.add('d-none');
    }

    renderEmailAccountCredentialState(null);
    renderEmailOAuthConnectPanel();
    if (options.refreshList !== false) {
      renderEmailAccountsList(emailAccounts);
    }
    renderEmailAccountsSummary();
  }

  function applyEmailAccountToForm(account) {
    if (!account || typeof account !== 'object') {
      clearEmailAccountForm();
      return;
    }

    selectedEmailAccount = account;
    if (emailAccountLabelInput) emailAccountLabelInput.value = account.label || '';
    if (emailAccountAddressInput) emailAccountAddressInput.value = account.email_address || '';
    if (emailAccountProviderInput) emailAccountProviderInput.value = normalizeEmailProvider(account.provider || 'gmail') || 'gmail';
    if (emailAccountAuthTypeInput) emailAccountAuthTypeInput.value = normalizeEmailAuthType(account.auth_type || 'oauth2') || 'oauth2';
    if (emailAccountDisplayNameInput) emailAccountDisplayNameInput.value = account.display_name || '';
    if (emailAccountUsernameInput) emailAccountUsernameInput.value = account.username || '';
    if (emailAccountWorkspaceInput) emailAccountWorkspaceInput.value = account.workspace_id || '';
    if (emailAccountTagsInput) emailAccountTagsInput.value = Array.isArray(account.tags) ? account.tags.join(', ') : '';
    if (emailAccountSourceInput) emailAccountSourceInput.value = account.source || '';
    if (emailAccountRetentionInput) emailAccountRetentionInput.value = account.retention_policy || '';
    if (emailAccountImapHostInput) emailAccountImapHostInput.value = account.imap_host || '';
    if (emailAccountImapPortInput) emailAccountImapPortInput.value = account.imap_port ? String(account.imap_port) : '';
    if (emailAccountSmtpHostInput) emailAccountSmtpHostInput.value = account.smtp_host || '';
    if (emailAccountSmtpPortInput) emailAccountSmtpPortInput.value = account.smtp_port ? String(account.smtp_port) : '';
    if (emailAccountOauthAdvancedEl) emailAccountOauthAdvancedEl.open = false;
    resetEmailCredentialInputs();
    syncEmailAccountProviderFields();
    syncEmailAccountAuthFields();

    if (emailAccountModeBadgeEl) {
      emailAccountModeBadgeEl.textContent = emailAccountHasStoredConnection(account) ? 'Connected account' : 'Editing account';
    }
    if (deleteEmailAccountBtn) {
      deleteEmailAccountBtn.classList.remove('d-none');
    }

    renderEmailAccountCredentialState(account);
    renderEmailOAuthConnectPanel();
    renderEmailAccountsList(emailAccounts);
    renderEmailAccountsSummary();
  }

  function selectEmailAccount(accountID) {
    const match = emailAccounts.find((account) => account.id === accountID) || null;
    if (!match) {
      clearEmailAccountForm();
      return;
    }
    applyEmailAccountToForm(match);
  }

  async function loadEmailAccounts(preferredAccountID = '') {
    if (!vaultStatus?.available || vaultStatus?.locked || !currentVaultID()) {
      emailAccounts = [];
      renderEmailAccountsList([]);
      clearEmailAccountForm({ refreshList: false });
      renderEmailAccountsSummary();
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/email-accounts'));
    emailAccounts = Array.isArray(data.accounts) ? data.accounts : [];
    renderEmailAccountsList(emailAccounts);

    const selectedID = String(preferredAccountID || selectedEmailAccount?.id || '').trim();
    if (selectedID) {
      const match = emailAccounts.find((account) => account.id === selectedID) || null;
      if (match) {
        applyEmailAccountToForm(match);
      } else {
        clearEmailAccountForm({ refreshList: false });
      }
    } else {
      clearEmailAccountForm({ refreshList: false });
    }

    renderEmailAccountsSummary();
  }

  async function loadEmailOAuthProviders() {
    try {
      const data = await apiRequest('/api/vault/email-oauth/providers');
      const nextProviders = {};
      const items = Array.isArray(data.providers) ? data.providers : [];
      items.forEach((item) => {
        const key = normalizeEmailProvider(item.provider);
        if (key) {
          nextProviders[key] = item;
        }
      });
      emailOAuthProviders = nextProviders;
      emailOAuthProviderLoadError = '';
    } catch (error) {
      console.error('Failed to load email OAuth providers:', error);
      emailOAuthProviders = {};
      emailOAuthProviderLoadError = error.message || 'Ori could not load OAuth provider status.';
    }

    renderEmailOAuthConnectPanel();
    renderEmailAccountsList(emailAccounts);
  }

  function buildEmailAccountBasePayload() {
    return {
      label: String(emailAccountLabelInput?.value || '').trim(),
      workspace_id: String(emailAccountWorkspaceInput?.value || '').trim(),
      tags: parseTags(emailAccountTagsInput?.value || ''),
      source: String(emailAccountSourceInput?.value || '').trim(),
      retention_policy: String(emailAccountRetentionInput?.value || '').trim(),
      provider: normalizeEmailProvider(emailAccountProviderInput?.value),
      email_address: String(emailAccountAddressInput?.value || '').trim(),
      display_name: String(emailAccountDisplayNameInput?.value || '').trim(),
      username: String(emailAccountUsernameInput?.value || '').trim(),
      auth_type: normalizeEmailAuthType(emailAccountAuthTypeInput?.value),
      imap_host: String(emailAccountImapHostInput?.value || '').trim(),
      imap_port: parseOptionalPort(emailAccountImapPortInput?.value),
      smtp_host: String(emailAccountSmtpHostInput?.value || '').trim(),
      smtp_port: parseOptionalPort(emailAccountSmtpPortInput?.value)
    };
  }

  function buildEmailOAuthStartURL() {
    const provider = normalizeEmailProvider(emailAccountProviderInput?.value);
    const providerStatus = currentEmailOAuthProviderStatus(provider);
    const basePayload = buildEmailAccountBasePayload();

    if (!currentVaultID()) {
      throw new Error('Create or select a vault before connecting an email account.');
    }
    if (!vaultStatus?.available || vaultStatus?.locked) {
      throw new Error('Unlock the selected vault before connecting an email account.');
    }
    if (!currentEmailOAuthConnectMode()) {
      throw new Error('Choose OAuth 2 for Google or Microsoft before starting the connect flow.');
    }
    if (!providerStatus.enabled) {
      throw new Error(providerStatus.reason || 'OAuth is not configured for this provider.');
    }
    if (!basePayload.email_address) {
      throw new Error('Email address is required before connecting the account.');
    }

    const params = new URLSearchParams();
    params.set('vault_id', currentVaultID());
    params.set('provider', provider);
    params.set('email_address', basePayload.email_address);

    if (basePayload.label) params.set('label', basePayload.label);
    if (basePayload.display_name) params.set('display_name', basePayload.display_name);
    if (basePayload.username) params.set('username', basePayload.username);
    if (basePayload.workspace_id) params.set('workspace_id', basePayload.workspace_id);
    if (Array.isArray(basePayload.tags) && basePayload.tags.length) params.set('tags', basePayload.tags.join(','));
    if (basePayload.source) params.set('source', basePayload.source);
    if (basePayload.retention_policy) params.set('retention_policy', basePayload.retention_policy);
    if (selectedEmailAccount?.id && normalizeEmailProvider(selectedEmailAccount.provider) === provider) {
      params.set('account_id', selectedEmailAccount.id);
    }

    return {
      url: `/api/vault/email-oauth/start?${params.toString()}`,
      providerLabel: oauthProviderButtonLabel(provider)
    };
  }

  async function startEmailOAuthFlow() {
    try {
      const start = buildEmailOAuthStartURL();
      clearEmailOAuthPopupState();
      setButtonLoading(emailAccountConnectBtn, true, `Opening ${start.providerLabel}`);
      emailOAuthPending = true;

      const popup = window.open(start.url, 'ori-email-oauth', 'popup=yes,width=560,height=760');
      if (!popup) {
        clearEmailOAuthPopupState();
        showInlineAlert('Ori could not open the connection window. Allow popups and try again.', 'warning');
        return;
      }

      emailOAuthPopup = popup;
      popup.focus();

      emailOAuthPopupPollID = window.setInterval(() => {
        if (!emailOAuthPopup || emailOAuthPopup.closed) {
          const wasPending = emailOAuthPending;
          clearEmailOAuthPopupState();
          renderEmailOAuthConnectPanel();
          if (wasPending) {
            showInlineAlert('Connection window closed before the account was connected.', 'warning');
          }
        }
      }, 400);
    } catch (error) {
      clearEmailOAuthPopupState();
      showInlineAlert(error.message || 'Failed to start the email connection flow.', 'error');
    }
  }

  async function saveEmailAccount() {
    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before saving an email account.', 'warning');
      return;
    }
    if (!vaultStatus?.available || vaultStatus?.locked) {
      showInlineAlert('Unlock the selected vault before saving an email account.', 'warning');
      return;
    }

    const basePayload = buildEmailAccountBasePayload();
    if (!basePayload.email_address) {
      showInlineAlert('Email address is required before saving the account.', 'warning');
      return;
    }

    const connectMode = currentEmailOAuthConnectMode();
    const manualTokenProvided = Boolean(
      String(emailAccountAccessTokenInput?.value || '').trim()
      || String(emailAccountRefreshTokenInput?.value || '').trim()
    );
    if (connectMode && !selectedEmailAccount?.id && !manualTokenProvided) {
      showInlineAlert(`This account is not connected yet. Use Connect with ${oauthProviderButtonLabel(basePayload.provider)} or open Advanced OAuth import to paste existing tokens.`, 'warning');
      return;
    }

    try {
      setButtonLoading(saveEmailAccountBtn, true, selectedEmailAccount ? 'Saving account' : 'Saving account');

      let response;
      if (selectedEmailAccount?.id) {
        const updatePayload = { ...basePayload };
        const accessToken = String(emailAccountAccessTokenInput?.value || '').trim();
        const refreshToken = String(emailAccountRefreshTokenInput?.value || '').trim();
        const password = String(emailAccountPasswordInput?.value || '').trim();
        const clientID = String(emailAccountClientIdInput?.value || '').trim();
        const clientSecret = String(emailAccountClientSecretInput?.value || '').trim();
        const tokenEndpoint = String(emailAccountTokenEndpointInput?.value || '').trim();

        if (accessToken) updatePayload.access_token = accessToken;
        if (refreshToken) updatePayload.refresh_token = refreshToken;
        if (password) updatePayload.password = password;
        if (clientID) updatePayload.client_id = clientID;
        if (clientSecret) updatePayload.client_secret = clientSecret;
        if (tokenEndpoint) updatePayload.token_endpoint = tokenEndpoint;

        response = await apiRequest(`/api/vault/email-accounts/${encodeURIComponent(selectedEmailAccount.id)}`, {
          method: 'PATCH',
          body: updatePayload
        });
      } else {
        response = await apiRequest('/api/vault/email-accounts', {
          method: 'POST',
          body: {
            ...basePayload,
            vault_id: currentVaultID(),
            credentials: {
              access_token: String(emailAccountAccessTokenInput?.value || '').trim(),
              refresh_token: String(emailAccountRefreshTokenInput?.value || '').trim(),
              password: String(emailAccountPasswordInput?.value || '').trim(),
              client_id: String(emailAccountClientIdInput?.value || '').trim(),
              client_secret: String(emailAccountClientSecretInput?.value || '').trim(),
              token_endpoint: String(emailAccountTokenEndpointInput?.value || '').trim()
            }
          }
        });
      }

      notify(selectedEmailAccount ? 'Email account updated.' : 'Email account saved.', 'success');
      const nextAccountID = String(response?.account?.id || '').trim();
      await refreshVault();
      if (nextAccountID) {
        selectEmailAccount(nextAccountID);
      }
    } catch (error) {
      console.error('Failed to save email account:', error);
      showInlineAlert(error.message || 'Failed to save email account.', 'error');
    } finally {
      setButtonLoading(saveEmailAccountBtn, false);
    }
  }

  async function deleteEmailAccount() {
    if (!selectedEmailAccount?.id) {
      showInlineAlert('Select an email account before deleting it.', 'warning');
      return;
    }

    const confirmed = window.confirm(
      `Delete email account "${selectedEmailAccount.label || selectedEmailAccount.email_address}" from ${vaultDisplayLabel(currentVault())}?`
    );
    if (!confirmed) {
      return;
    }

    try {
      setButtonLoading(deleteEmailAccountBtn, true, 'Deleting');
      await apiRequest(`/api/vault/email-accounts/${encodeURIComponent(selectedEmailAccount.id)}`, {
        method: 'DELETE'
      });
      notify('Email account deleted.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to delete email account:', error);
      showInlineAlert(error.message || 'Failed to delete email account.', 'error');
    } finally {
      setButtonLoading(deleteEmailAccountBtn, false);
    }
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

  async function loadVaultFolders() {
    if (!vaultStatus?.available || vaultStatus?.locked) {
      folders = [];
      rebuildFolderTree();
      renderExplorer();
      return;
    }

    const data = await apiRequest(vaultURL('/api/vault/folders'));
    folders = Array.isArray(data.folders) ? data.folders : [];
    rebuildFolderTree();
    renderExplorer();
  }

  async function loadVaultRecords() {
    if (!vaultStatus?.available || vaultStatus?.locked) {
      records = [];
      folders = [];
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
    const selectedEmailAccountID = selectedEmailAccount?.id || '';

    try {
      await Promise.all([loadVaults(), loadEmailOAuthProviders()]);
      const status = await loadVaultStatus();
      if (!status.available) {
        renderVaultSpaces();
        folders = [];
        renderGrantsList([]);
        renderRecordsList([]);
        renderEmailAccountsList([]);
        clearEmailAccountForm({ refreshList: false });
        renderEmailAccountsSummary();
        rebuildFolderTree();
        renderExplorer();
        return;
      }
      await loadVaultGrants();
      if (!status.locked) {
        await Promise.all([loadVaultFolders(), loadVaultRecords()]);
        await loadEmailAccounts(selectedEmailAccountID);
        if (selectedRecordID && recordIndex.has(selectedRecordID)) {
          await selectRecord(selectedRecordID, { keepFolder: true, openEditor: editorDialogOpen });
        } else {
          clearRecordForm({ preserveStatus: true, refreshList: true, refreshExplorer: true });
        }
      } else {
        renderVaultSpaces();
        renderEmailAccountsSummary();
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
      const openEditor = options.openEditor !== false;
      if (!keepFolder) {
        const activeFolder = selectedFolder();
        const folderHasRecord = activeFolder && Array.isArray(activeFolder.recordIDs) && activeFolder.recordIDs.includes(record.id);
        if (!folderHasRecord) {
          selectedFolderPath = folderNodePath(record.folder_path || '');
        }
      }
      if (openEditor) {
        applyRecordToForm(record);
      } else {
        selectedRecord = record;
        payloadRevealed = false;
        renderRecordsList(records);
      }
      renderExplorer();
    } catch (error) {
      console.error('Failed to load vault record:', error);
      showInlineAlert(error.message || 'Failed to load vault record.', 'error');
    }
  }

  function focusEditor() {
    showVaultEditor();
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
      openUnlockDialog();
      return;
    }

    await lockVault();
  }

  async function uploadRecordAttachment(recordID, attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      throw new Error('One of the attachments is invalid.');
    }

    const formData = new window.FormData();
    formData.append('file', attachmentBlob(normalized), normalized.name || 'attachment');
    if (normalized.kind) {
      formData.append('kind', normalized.kind);
    }

    return apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(recordID)}/attachments`), {
      method: 'POST',
      body: formData
    });
  }

  async function syncRecordAttachments(recordID, previousAttachments, nextAttachments) {
    if (!recordID) {
      return;
    }

    const previous = cloneEntryAttachments(previousAttachments);
    const next = cloneEntryAttachments(nextAttachments);
    const nextStoredIDs = new Set(next.filter(hasStoredAttachment).map((attachment) => attachment.id));

    for (const attachment of next) {
      if (hasStoredAttachment(attachment)) {
        continue;
      }

      await uploadRecordAttachment(recordID, attachment);
    }

    for (const attachment of previous) {
      if (!hasStoredAttachment(attachment) || nextStoredIDs.has(attachment.id)) {
        continue;
      }

      await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(recordID)}/attachments/${encodeURIComponent(attachment.id)}`), {
        method: 'DELETE'
      });
    }
  }

  async function saveRecord() {
    const editing = Boolean(selectedRecord?.id);

    if (!currentVaultID()) {
      showInlineAlert('Create or select a vault before saving an entry.', 'warning');
      return;
    }

    if (!vaultStatus?.available || vaultStatus?.locked) {
      showInlineAlert(editing ? 'Unlock the selected vault before editing an entry.' : 'Unlock the selected vault before creating a new entry.', 'warning');
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
      showInlineAlert('Title is required before saving to the vault.', 'warning');
      return;
    }

    try {
      payloadBody.folder_path = normalizeFolderPathInput(entryFolderPathInput?.value);
      payloadBody.payload = payloadWithoutAttachments(parsePayloadInput());
    } catch (error) {
      showInlineAlert(error.message || 'Payload must be valid JSON.', 'warning');
      return;
    }

    let persistedRecordID = selectedRecord?.id || '';
    let recordPersisted = false;

    try {
      setButtonLoading(saveEntryBtn, true, editing ? 'Updating entry' : 'Saving entry');

      let response;
      if (editing) {
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
      persistedRecordID = persistedRecordID || String(response?.record?.id || '').trim();
      recordPersisted = true;

      await syncRecordAttachments(persistedRecordID, entryAttachmentSnapshot, entryAttachments);

      closeVaultEditor({ restoreFocus: false });

      if (editing) {
        notify('Vault entry updated.', 'success');
        await refreshVault();
      } else {
        clearRecordForm({ hide: true, refreshList: false, refreshExplorer: false });
        selectedFolderPath = folderNodePath(payloadBody.folder_path);
        notify('Vault entry saved.', 'success');
        await refreshVault();

        const createdRecordID = response?.record?.id;
        if (createdRecordID) {
          await selectRecord(createdRecordID, { openEditor: false });
        }
      }
    } catch (error) {
      console.error('Failed to save vault entry:', error);
      if (recordPersisted) {
        closeVaultEditor({ restoreFocus: false });
        notify(editing ? 'Vault entry updated, but attachments need attention.' : 'Vault entry saved, but attachments need attention.', 'warning');
        if (editing) {
          await refreshVault();
        } else {
          await refreshVault();
          if (persistedRecordID) {
            await selectRecord(persistedRecordID, { openEditor: false });
          }
        }
        showInlineAlert(error.message || 'The entry was saved, but attachments failed to sync.', 'error');
        return;
      }
      showInlineAlert(error.message || (editing ? 'Failed to update vault entry.' : 'Failed to save vault entry.'), 'error');
    } finally {
      setButtonLoading(saveEntryBtn, false);
    }
  }

  async function deleteRecord() {
    if (!selectedRecord) {
      return;
    }

    const confirmed = window.confirm(`Delete vault item "${selectedRecord.label || selectedRecord.id}"?`);
    if (!confirmed) {
      return;
    }

    try {
      setButtonLoading(deleteEntryBtn, true, 'Deleting');
      await apiRequest(vaultURL(`/api/vault/records/${encodeURIComponent(selectedRecord.id)}`), {
        method: 'DELETE'
      });
      notify('Vault item deleted.', 'success');
      clearRecordForm({ hide: true });
      await refreshVault();
    } catch (error) {
      console.error('Failed to delete vault item:', error);
      showInlineAlert(error.message || 'Failed to delete vault item.', 'error');
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
      closeUnlockDialog({ restoreFocus: false });
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
      setButtonLoading(null, true, 'Locking');
      await apiRequest(vaultURL('/api/vault/lock'), {
        method: 'POST',
        body: {}
      });
      closeUnlockDialog({ restoreFocus: false });
      notify('Vault locked.', 'success');
      await refreshVault();
    } catch (error) {
      console.error('Failed to lock vault:', error);
      showInlineAlert(error.message || 'Failed to lock vault.', 'error');
    } finally {
      setButtonLoading(null, false);
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
      closeExportDialog({ restoreFocus: false });
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

    const requestBody = new window.FormData();
    requestBody.append('file', file, String(file.name || 'vault-export.json'));
    requestBody.append('import_password', importPassword);
    requestBody.append('restore_grants', String(Boolean(importRestoreGrantsInput?.checked)));

    if (mode === 'current') {
      requestBody.append('target_vault_id', currentVaultID());
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
      requestBody.append('create_vault_name', String(importVaultNameInput?.value || '').trim());
      requestBody.append('create_vault_description', String(importVaultDescriptionInput?.value || '').trim());
      requestBody.append('create_vault_password', vaultPassword);
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

      closeImportDialog({ restoreFocus: false });
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

  openCreateDialogBtn?.addEventListener('click', () => {
    openCreateDialog();
  });

  openExportDialogBtn?.addEventListener('click', () => {
    openExportDialog();
  });

  openImportDialogBtn?.addEventListener('click', () => {
    openImportDialog();
  });

  unlockBtn?.addEventListener('click', () => {
    unlockVault();
  });

  unlockCancelBtn?.addEventListener('click', () => {
    closeUnlockDialog();
  });

  unlockOverlay?.addEventListener('click', (event) => {
    const dismissTrigger = event.target.closest('[data-action="dismiss-page-unlock-dialog"]');
    if (dismissTrigger) {
      closeUnlockDialog();
    }
  });

  createCancelBtn?.addEventListener('click', () => {
    closeCreateDialog();
  });

  createOverlay?.addEventListener('click', (event) => {
    const dismissTrigger = event.target.closest('[data-action="dismiss-page-create-dialog"]');
    if (dismissTrigger) {
      closeCreateDialog();
    }
  });

  unlockPasswordInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      unlockVault();
    }
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
    if (selectedRecord) {
      applyRecordToForm(selectedRecord);
      return;
    }
    clearRecordForm();
    showVaultEditor();
  });

  explorerAddBtn?.addEventListener('click', () => {
    clearRecordForm();
    showVaultEditor();
  });

  explorerNewFolderBtn?.addEventListener('click', () => {
    openFolderComposer();
  });

  editorCloseBtn?.addEventListener('click', () => {
    closeVaultEditor();
  });

  editorOverlay?.addEventListener('click', (event) => {
    const dismissTrigger = event.target.closest('[data-action="dismiss-page-entry-dialog"]');
    if (dismissTrigger) {
      closeVaultEditor();
    }
  });

  revealPayloadBtn?.addEventListener('click', () => {
    togglePayloadReveal();
  });

  deleteEntryBtn?.addEventListener('click', () => {
    deleteRecord();
  });

  entryTypeInput?.addEventListener('change', () => {
    syncEntryComposerPresentation();
  });

  entryJsonModeInput?.addEventListener('change', () => {
    syncEntryComposerPresentation();
  });

  entryUseSelectedFolderBtn?.addEventListener('click', () => {
    if (entryFolderPathInput) {
      entryFolderPathInput.value = selectedFolderRelativePath();
    }
  });

  entryAttachBtn?.addEventListener('click', () => {
    entryAttachmentsInput?.click();
  });

  entryAttachmentsInput?.addEventListener('change', (event) => {
    addEntryAttachments(event.target.files).catch((error) => {
      console.error('Failed to add vault attachments:', error);
      showInlineAlert(error.message || 'Failed to add attachment.', 'error');
    });
  });

  entryAttachmentsList?.addEventListener('click', (event) => {
    const target = event.target.closest('[data-action="remove-entry-attachment"]');
    if (!target) {
      return;
    }

    const attachmentID = target.getAttribute('data-attachment-id');
    if (attachmentID) {
      removeEntryAttachment(attachmentID);
    }
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
      return;
    }

    if (action.getAttribute('data-action') === 'select-tree-record') {
      selectedFolderPath = folderPath || ROOT_FOLDER_PATH;
      selectRecord(action.getAttribute('data-record-id'), { keepFolder: true, openEditor: false });
      return;
    }

    if (action.getAttribute('data-action') === 'open-folder-composer') {
      openFolderComposer();
      return;
    }

    if (action.getAttribute('data-action') === 'save-folder-composer') {
      createFolderFromComposer();
      return;
    }

    if (action.getAttribute('data-action') === 'cancel-folder-composer') {
      closeFolderComposer();
    }
  });

  folderTreeEl?.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter') {
      return;
    }
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.matches('[data-folder-create-input]')) {
      return;
    }
    event.preventDefault();
    createFolderFromComposer();
  });

  folderTreeEl?.addEventListener('dragstart', (event) => {
    const button = dragRecordButtonFromTarget(event.target);
    if (!button) {
      return;
    }

    beginRecordDrag(button, event);
  });

  folderTreeEl?.addEventListener('dragend', () => {
    clearRecordDrag();
  });

  folderTreeEl?.addEventListener('dragover', (event) => {
    if (!draggedRecordID) {
      return;
    }

    const folderButton = folderDropButtonFromTarget(event.target);
    if (!folderButton) {
      clearFolderDropTarget();
      return;
    }

    event.preventDefault();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect = 'move';
    }
    setFolderDropTarget(folderButton);
  });

  folderTreeEl?.addEventListener('dragleave', (event) => {
    if (!draggedRecordID) {
      return;
    }

    const nextTarget = event.relatedTarget;
    if (nextTarget instanceof Node && folderTreeEl.contains(nextTarget)) {
      return;
    }

    clearFolderDropTarget();
  });

  folderTreeEl?.addEventListener('drop', async (event) => {
    if (!draggedRecordID) {
      return;
    }

    const folderButton = folderDropButtonFromTarget(event.target);
    const recordID = draggedRecordID;
    clearRecordDrag();

    if (!folderButton) {
      return;
    }

    event.preventDefault();
    await moveRecordToFolder(recordID, folderPathFromNodePath(folderButton.getAttribute('data-folder-path')));
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

  explorerPreviewEl?.addEventListener('dragstart', (event) => {
    const button = dragRecordButtonFromTarget(event.target);
    if (!button) {
      return;
    }

    beginRecordDrag(button, event);
  });

  explorerPreviewEl?.addEventListener('dragend', () => {
    clearRecordDrag();
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

  window.addEventListener('message', async (event) => {
    if (event.origin !== window.location.origin) {
      return;
    }

    const payload = event.data;
    if (!payload || payload.type !== 'ori:vault-email-oauth') {
      return;
    }

    clearEmailOAuthPopupState();
    renderEmailOAuthConnectPanel();

    if (!payload.success) {
      showInlineAlert(payload.error || 'Ori could not connect the email account.', 'error');
      return;
    }

    notify(payload.message || 'Email account connected.', 'success');
    await refreshVault();

    const accountID = String(payload.account?.id || '').trim();
    if (accountID) {
      selectEmailAccount(accountID);
    }
  });

  emailAccountProviderInput?.addEventListener('change', () => {
    syncEmailAccountProviderFields();
  });

  emailAccountAuthTypeInput?.addEventListener('change', () => {
    syncEmailAccountAuthFields();
  });

  emailAccountConnectBtn?.addEventListener('click', () => {
    startEmailOAuthFlow();
  });

  saveEmailAccountBtn?.addEventListener('click', () => {
    saveEmailAccount();
  });

  resetEmailAccountBtn?.addEventListener('click', () => {
    clearEmailAccountForm();
  });

  deleteEmailAccountBtn?.addEventListener('click', () => {
    deleteEmailAccount();
  });

  emailAccountsListEl?.addEventListener('click', (event) => {
    const trigger = event.target.closest('[data-email-account-id]');
    if (!trigger) {
      return;
    }
    selectEmailAccount(trigger.getAttribute('data-email-account-id'));
  });

  exportBtn?.addEventListener('click', () => {
    exportVault();
  });

  exportCancelBtn?.addEventListener('click', () => {
    closeExportDialog();
  });

  exportOverlay?.addEventListener('click', (event) => {
    const dismissTrigger = event.target.closest('[data-action="dismiss-page-export-dialog"]');
    if (dismissTrigger) {
      closeExportDialog();
    }
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
    syncImportDialog();
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

  importCancelBtn?.addEventListener('click', () => {
    closeImportDialog();
  });

  importOverlay?.addEventListener('click', (event) => {
    const dismissTrigger = event.target.closest('[data-action="dismiss-page-import-dialog"]');
    if (dismissTrigger) {
      closeImportDialog();
    }
  });

  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') {
      return;
    }

    if (editorDialogOpen) {
      closeVaultEditor();
      return;
    }

    if (unlockDialogOpen) {
      closeUnlockDialog();
      return;
    }

    if (createDialogOpen) {
      closeCreateDialog();
      return;
    }

    if (exportDialogOpen) {
      closeExportDialog();
      return;
    }

    if (importDialogOpen) {
      closeImportDialog();
    }
  });

  // Tab Persistence
  const STORAGE_KEY_TAB = 'ori-vault-active-tab';
  const tabEls = document.querySelectorAll('#vaultTab button[data-bs-toggle="tab"]');
  if (tabEls.length && typeof bootstrap !== 'undefined' && bootstrap.Tab) {
    tabEls.forEach(tabEl => {
      tabEl.addEventListener('shown.bs.tab', (event) => {
        localStorage.setItem(STORAGE_KEY_TAB, event.target.id);
      });
    });

    const activeTabId = localStorage.getItem(STORAGE_KEY_TAB);
    if (activeTabId) {
      const activeTab = document.getElementById(activeTabId);
      if (activeTab) {
        const tabInstance = bootstrap.Tab.getOrCreateInstance(activeTab);
        tabInstance.show();
      }
    }
  }

  clearRecordForm();
  clearEmailAccountForm({ refreshList: false });
  syncEmailAccountProviderFields();
  syncEmailAccountAuthFields();
  syncImportControls();
  syncPageDialogs();
  refreshVault();
})();

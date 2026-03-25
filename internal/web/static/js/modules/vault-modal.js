(function() {
  const ROOT_FOLDER_PATH = 'vault';
  const TYPE_FOLDER_PATH = 'types';
  const WORKSPACE_FOLDER_PATH = 'workspaces';
  const DEFAULT_VAULT_ID = '';
  const VAULT_STORAGE_KEY = 'ori-selected-vault-id';
  const DEFAULT_EXPANDED_FOLDERS = new Set([ROOT_FOLDER_PATH, TYPE_FOLDER_PATH, WORKSPACE_FOLDER_PATH]);
  const MAX_ENTRY_ATTACHMENTS = 6;
  const MAX_ENTRY_ATTACHMENT_BYTES = 10 * 1024 * 1024;

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
    unlockDialogOpen: false,
    createDialogOpen: false,
    exportDialogOpen: false,
    entryDialogOpen: false,
    entryDialogMode: 'create',
    entryDialogRecord: null,
    entryAttachments: []
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

  function escapeSelectorValue(value) {
    const normalized = String(value || '');
    if (window.CSS && typeof window.CSS.escape === 'function') {
      return window.CSS.escape(normalized);
    }

    return normalized.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
  }

  function slugifyFilename(value) {
    return String(value || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'vault';
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
      workspaceKicker: document.getElementById('vaultModalWorkspaceKicker'),
      workspaceTitle: document.getElementById('vaultModalWorkspaceTitle'),
      workspaceDescription: document.getElementById('vaultModalWorkspaceDescription'),
      loadingState: document.getElementById('vaultModalLoadingState'),
      vaultSelectionStack: document.getElementById('vaultModalVaultSelectionStack'),
      selectedVaultState: document.getElementById('vaultModalSelectedVaultState'),
      vaultHint: document.getElementById('vaultModalVaultHint'),
      vaultManageStack: document.getElementById('vaultModalVaultManageStack'),
      openEntryBtn: document.getElementById('vaultModalOpenEntryBtn'),
      openCreateVaultBtn: document.getElementById('vaultModalOpenCreateVaultBtn'),
      openExportBtn: document.getElementById('vaultModalOpenExportBtn'),
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
      createOverlay: document.getElementById('vaultModalCreateOverlay'),
      createDialogTitle: document.getElementById('vaultModalCreateDialogTitle'),
      createDialogDescription: document.getElementById('vaultModalCreateDialogDescription'),
      createCancelBtn: document.getElementById('vaultModalCreateCancelBtn'),
      exportOverlay: document.getElementById('vaultModalExportOverlay'),
      exportDialogTitle: document.getElementById('vaultModalExportDialogTitle'),
      exportDialogDescription: document.getElementById('vaultModalExportDialogDescription'),
      exportVaultName: document.getElementById('vaultModalExportVaultName'),
      exportWorkspaceInput: document.getElementById('vaultModalExportWorkspaceId'),
      exportPasswordInput: document.getElementById('vaultModalExportPassword'),
      exportConfirmInput: document.getElementById('vaultModalExportConfirm'),
      exportBtn: document.getElementById('vaultModalExportBtn'),
      exportCancelBtn: document.getElementById('vaultModalExportCancelBtn'),
      entryOverlay: document.getElementById('vaultModalEntryOverlay'),
      entryDialogTitle: document.getElementById('vaultModalEntryDialogTitle'),
      entryCancelBtn: document.getElementById('vaultModalEntryCancelBtn'),
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
      entryAdvancedDetails: document.getElementById('vaultModalEntryAdvanced'),
      entryContentField: document.getElementById('vaultModalEntryContentField'),
      entryContentInput: document.getElementById('vaultModalEntryContent'),
      entryContentHelp: document.getElementById('vaultModalEntryContentHelp'),
      entryAttachBtn: document.getElementById('vaultModalEntryAttachBtn'),
      entryAttachmentsInput: document.getElementById('vaultModalEntryAttachmentsInput'),
      entryAttachmentsList: document.getElementById('vaultModalEntryAttachmentsList'),
      entryJsonModeInput: document.getElementById('vaultModalEntryJsonMode'),
      entryTagsInput: document.getElementById('vaultModalEntryTags'),
      entrySourceInput: document.getElementById('vaultModalEntrySource'),
      entryRetentionInput: document.getElementById('vaultModalEntryRetention'),
      entryPayloadField: document.getElementById('vaultModalEntryPayloadField'),
      entryPayloadInput: document.getElementById('vaultModalEntryPayload'),
      saveBtn: document.getElementById('vaultModalSaveBtn'),
      resetBtn: document.getElementById('vaultModalResetBtn'),
      mainGrid: document.getElementById('vaultModalMainGrid'),
      folderTabPanel: document.getElementById('vaultModalFolderTabPanel'),
      folderVaultTabs: document.getElementById('vaultModalFolderVaultTabs'),
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
    return size.toFixed(digits) + ' ' + units[unitIndex];
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

    return 'attachment-' + String(Date.now()) + '-' + String(Math.random()).slice(2, 8);
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
      name: name,
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
    Object.keys(payload).forEach(function(key) {
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
      next.attachments = attachments.map(function(item) {
        return {
          id: item.id,
          name: item.name,
          mime_type: item.mime_type,
          size_bytes: item.size_bytes,
          kind: item.kind
        };
      });
    }
    return next;
  }

  function attachmentDataURL(attachment) {
    const mimeType = String(attachment?.mime_type || 'application/octet-stream').trim() || 'application/octet-stream';
    const contentBase64 = String(attachment?.content_base64 || '').trim();
    return contentBase64 ? 'data:' + mimeType + ';base64,' + contentBase64 : '';
  }

  function readFileAsBase64(file) {
    return new Promise(function(resolve, reject) {
      const reader = new FileReader();

      reader.onload = function() {
        const result = String(reader.result || '');
        const commaIndex = result.indexOf(',');
        resolve(commaIndex >= 0 ? result.slice(commaIndex + 1) : result);
      };

      reader.onerror = function() {
        reject(new Error('Failed to read file "' + String(file?.name || 'attachment') + '".'));
      };

      reader.readAsDataURL(file);
    });
  }

  function totalAttachmentBytes(attachments) {
    return (Array.isArray(attachments) ? attachments : []).reduce(function(total, attachment) {
      return total + Number(attachment?.size_bytes || 0);
    }, 0);
  }

  function renderEntryAttachments() {
    if (!elements?.entryAttachmentsList) {
      return;
    }

    if (!state.entryAttachments.length) {
      elements.entryAttachmentsList.innerHTML = '<div class="vault-modal-attachments-empty">No files or images attached yet.</div>';
      return;
    }

    elements.entryAttachmentsList.innerHTML = state.entryAttachments.map(function(attachment) {
      return (
        '<div class="vault-modal-attachment-item">' +
          '<div class="vault-modal-attachment-item-main">' +
            '<span class="vault-modal-attachment-icon">' + attachmentIcon(attachment.kind) + '</span>' +
            '<div class="vault-modal-attachment-copy">' +
              '<div class="vault-modal-attachment-name">' + escapeHTML(attachment.name) + '</div>' +
              '<div class="vault-modal-attachment-meta">' + escapeHTML([attachment.kind === 'image' ? 'Image' : 'File', formatBytes(attachment.size_bytes)].join(' • ')) + '</div>' +
            '</div>' +
          '</div>' +
          '<button type="button" class="modern-btn modern-btn-secondary vault-modal-attachment-remove" data-action="remove-entry-attachment" data-attachment-id="' + escapeHTML(attachment.id) + '">Remove</button>' +
        '</div>'
      );
    }).join('');
  }

  function mergeEntryAttachments(payload, attachments) {
    const normalizedAttachments = (Array.isArray(attachments) ? attachments : []).map(normalizeEntryAttachment).filter(Boolean);

    if (!normalizedAttachments.length) {
      return payloadWithoutAttachments(payload);
    }

    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      throw new Error('Structured payload must be a JSON object when files are attached.');
    }

    const next = payloadWithoutAttachments(payload);
    next.attachments = normalizedAttachments.map(function(attachment) {
      return {
        id: attachment.id,
        name: attachment.name,
        mime_type: attachment.mime_type,
        size_bytes: attachment.size_bytes,
        kind: attachment.kind,
        content_base64: attachment.content_base64
      };
    });
    return next;
  }

  async function addEntryAttachments(fileList) {
    const files = Array.from(fileList || []);
    if (!files.length) {
      return;
    }

    const nextAttachments = state.entryAttachments.slice();
    const rejected = [];

    if (nextAttachments.length >= MAX_ENTRY_ATTACHMENTS) {
      showAlert('You can attach up to ' + String(MAX_ENTRY_ATTACHMENTS) + ' files per vault entry.', 'warning');
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

      const contentBase64 = await readFileAsBase64(file);
      nextAttachments.push({
        id: generateAttachmentID(),
        name: String(file.name || 'attachment'),
        mime_type: String(file.type || 'application/octet-stream'),
        size_bytes: Number(file.size || 0),
        kind: attachmentKindForMimeType(file.type),
        content_base64: contentBase64
      });
    }

    state.entryAttachments = nextAttachments;
    renderEntryAttachments();

    if (elements?.entryAttachmentsInput) {
      elements.entryAttachmentsInput.value = '';
    }

    if (rejected.length) {
      showAlert('Some attachments were skipped. Entries support up to ' + String(MAX_ENTRY_ATTACHMENTS) + ' files and ' + formatBytes(MAX_ENTRY_ATTACHMENT_BYTES) + ' total.', 'warning');
      return;
    }

    showAlert('');
  }

  function removeEntryAttachment(attachmentID) {
    state.entryAttachments = state.entryAttachments.filter(function(item) {
      return item.id !== attachmentID;
    });
    renderEntryAttachments();
  }

  function downloadAttachment(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      showAlert('That attachment could not be opened.', 'warning');
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

  function parsePayloadInput() {
    const useJSON = Boolean(elements?.entryJsonModeInput?.checked);
    if (!useJSON) {
      return entrySimplePayload(elements?.entryTypeInput?.value, elements?.entryContentInput?.value);
    }

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
    return !normalized || ['personal_note', 'email_snippet', 'secret'].some(function(type) {
      return normalized === defaultPayloadValue(type);
    });
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
    const dataKeys = keys.filter(function(item) {
      return item !== 'attachments';
    });

    if (!dataKeys.length) {
      return true;
    }

    return dataKeys.length === 1 && dataKeys[0] === key && typeof payload[key] === 'string';
  }

  function isEditingEntryDialog() {
    return state.entryDialogMode === 'edit' && Boolean(state.entryDialogRecord?.id);
  }

  function resetEntryDialogState() {
    state.entryDialogMode = 'create';
    state.entryDialogRecord = null;
  }

  function populateEntryForm(record) {
    if (!elements || !record) {
      return;
    }

    const normalizedType = normalizeRecordType(record.type);
    const payload = record.payload ?? {};
    const payloadCore = payloadWithoutAttachments(payload) || {};
    const useJSON = !canUseSimpleEntryComposer(normalizedType, payload);
    const payloadValue = prettyPayload(payloadCore);

    elements.entryTypeInput.value = normalizedType;
    elements.entryWorkspaceInput.value = String(record.workspace_id || '');
    elements.entryLabelInput.value = String(record.label || '');
    elements.entryContentInput.value = entryContentFromPayload(normalizedType, payload);
    elements.entryJsonModeInput.checked = useJSON;
    elements.entryTagsInput.value = Array.isArray(record.tags) ? record.tags.join(', ') : '';
    elements.entrySourceInput.value = String(record.source || '');
    elements.entryRetentionInput.value = String(record.retention_policy || '');
    state.entryAttachments = entryAttachmentsFromPayload(payload);
    elements.entryPayloadInput.value = payloadValue === '{}' ? defaultPayloadValue(normalizedType) : payloadValue;

    if (elements.entryAdvancedDetails) {
      elements.entryAdvancedDetails.open = Boolean(
        record.workspace_id ||
        (Array.isArray(record.tags) && record.tags.length) ||
        record.source ||
        record.retention_policy
      );
    }

    renderEntryAttachments();
    syncEntryComposerPresentation();
  }

  function syncEntryComposerPresentation() {
    if (!elements) {
      return;
    }

    const normalizedType = normalizeRecordType(elements.entryTypeInput?.value || 'personal_note');
    const useJSON = Boolean(elements.entryJsonModeInput?.checked);

    if (elements.entryLabelInput) {
      elements.entryLabelInput.placeholder = entryLabelPlaceholder(normalizedType);
    }

    if (elements.entryContentInput) {
      elements.entryContentInput.placeholder = entryContentPlaceholder(normalizedType);
    }

    if (elements.entryContentHelp) {
      elements.entryContentHelp.textContent = useJSON
        ? 'Use JSON only if you want named fields or nested data.'
        : 'Write normal text here. Ori will turn it into an encrypted record for you.';
    }

    if (elements.entryContentField) {
      elements.entryContentField.hidden = useJSON;
    }

    if (elements.entryPayloadField) {
      elements.entryPayloadField.hidden = !useJSON;
    }

    if (useJSON && elements.entryPayloadInput) {
      const rawPayload = String(elements.entryPayloadInput.value || '').trim();
      if (isDefaultPayloadTemplate(rawPayload)) {
        const content = String(elements.entryContentInput?.value || '').trim();
        elements.entryPayloadInput.value = content
          ? prettyPayload(entrySimplePayload(normalizedType, content))
          : defaultPayloadValue(normalizedType);
      }
    } else if (!useJSON && elements.entryContentInput) {
      const currentContent = String(elements.entryContentInput.value || '').trim();
      const rawPayload = String(elements.entryPayloadInput?.value || '').trim();
      if (!currentContent && rawPayload) {
        try {
          const payload = JSON.parse(rawPayload);
          const extracted = entryContentFromPayload(normalizedType, payload);
          if (extracted) {
            elements.entryContentInput.value = extracted;
          }
        } catch (error) {
          // Keep the user's content area blank if the payload is intentionally custom.
        }
      }
    }
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

  function currentVaultActionButton() {
    return elements?.folderVaultTabs?.querySelector('[data-action="toggle-vault-access"]') ||
      elements?.vaultRail?.querySelector('[data-action="toggle-vault-access"]') ||
      null;
  }

  function currentVaultTabButton() {
    const activeID = activeVaultID();
    if (!activeID || !elements?.folderVaultTabs) {
      return null;
    }

    return Array.from(elements.folderVaultTabs.querySelectorAll('[data-action="select-folder-vault-tab"]')).find(function(button) {
      return button.getAttribute('data-vault-id') === activeID;
    }) || null;
  }

  function vaultLockIcon(locked) {
    if (locked) {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12,1A5,5 0 0,1 17,6V8H18A2,2 0 0,1 20,10V21A2,2 0 0,1 18,23H6A2,2 0 0,1 4,21V10A2,2 0 0,1 6,8H7V6A5,5 0 0,1 12,1M12,3A3,3 0 0,0 9,6V8H15V6A3,3 0 0,0 12,3M12,12A2,2 0 0,0 10,14A2,2 0 0,0 11,15.73V18H13V15.73A2,2 0 0,0 14,14A2,2 0 0,0 12,12Z"/></svg>';
    }

    return '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4.5" y="10" width="15" height="10" rx="2.5"></rect><path d="M9 10V7.6A3.6 3.6 0 0 1 15.3 5.2"></path><path d="M15.2 5.2L18 7.8"></path><path d="M12 14.2V16.2"></path></svg>';
  }

  function syncCreateDialog() {
    if (!elements?.createOverlay) {
      return;
    }

    const showEmptyCreateMode = state.hasHydrated && state.vaults.length === 0;
    const canShow = Boolean(state.createDialogOpen);

    if (elements.createDialogTitle) {
      elements.createDialogTitle.textContent = showEmptyCreateMode ? 'Create Your First Vault' : 'Create New Vault';
    }

    if (elements.createDialogDescription) {
      elements.createDialogDescription.textContent = showEmptyCreateMode
        ? 'Start with one encrypted vault. Each vault has its own password and protected record set.'
        : 'Create a separately encrypted vault with its own password.';
    }

    if (elements.openCreateVaultBtn) {
      elements.openCreateVaultBtn.textContent = showEmptyCreateMode ? 'Create First Vault' : 'New Vault';
    }

    if (elements.createVaultBtn && !elements.createVaultBtn.disabled) {
      elements.createVaultBtn.textContent = showEmptyCreateMode ? 'Create First Vault' : 'Create Vault';
    }

    elements.createOverlay.hidden = !canShow;
    elements.modal?.classList.toggle('has-create-dialog', canShow);
  }

  function resetExportForm() {
    if (!elements) {
      return;
    }

    if (elements.exportWorkspaceInput) {
      elements.exportWorkspaceInput.value = '';
    }
    if (elements.exportPasswordInput) {
      elements.exportPasswordInput.value = '';
    }
    if (elements.exportConfirmInput) {
      elements.exportConfirmInput.checked = false;
    }
  }

  function syncExportDialog() {
    if (!elements?.exportOverlay) {
      return;
    }

    const selectedVault = activeVault();
    const vaultLabel = vaultDisplayLabel(selectedVault);
    const canShow = Boolean(state.exportDialogOpen && state.hasHydrated && activeVaultID());

    if (elements.exportVaultName) {
      elements.exportVaultName.textContent = vaultLabel;
    }

    if (elements.exportDialogDescription) {
      elements.exportDialogDescription.textContent = 'Create a password-protected export bundle for ' + vaultLabel + ' so it can be moved or archived safely.';
    }

    elements.exportOverlay.hidden = !canShow;
    elements.modal?.classList.toggle('has-export-dialog', canShow);

    if (!canShow && elements.exportBtn && !elements.exportBtn.dataset.originalLabel) {
      resetExportForm();
    }
  }

  function syncEntryDialog() {
    if (!elements?.entryOverlay) {
      return;
    }

    const selectedVault = activeVault();
    const vaultLabel = vaultDisplayLabel(selectedVault);
    const canShow = Boolean(state.entryDialogOpen && state.status && state.status.available && !state.status.locked);
    const editing = isEditingEntryDialog();

    if (elements.entryDialogTitle) {
      elements.entryDialogTitle.textContent = editing ? 'Edit Vault Item' : 'New Vault Item';
    }

    if (elements.saveBtn && !elements.saveBtn.dataset.originalLabel) {
      elements.saveBtn.textContent = editing ? 'Update Entry' : 'Save Entry';
    }

    if (elements.resetBtn) {
      elements.resetBtn.textContent = editing ? 'Reset Changes' : 'Clear';
    }

    elements.entryOverlay.hidden = !canShow;
    elements.modal?.classList.toggle('has-entry-dialog', canShow);

    if (!canShow) {
      return;
    }

    syncEntryComposerPresentation();

    if (elements.entryLabelInput) {
      elements.entryLabelInput.setAttribute('aria-label', 'Label for ' + vaultLabel);
    }
  }

  function syncWorkspaceTabs() {
    if (!elements) {
      return;
    }

    if (elements.workspaceKicker) {
      elements.workspaceKicker.textContent = 'Encrypted Folder';
    }

    if (elements.workspaceTitle) {
      elements.workspaceTitle.textContent = 'Folder View';
    }

    if (elements.workspaceDescription) {
      elements.workspaceDescription.innerHTML = 'Browse the active vault like a protected folder. The vault tabs switch spaces directly, and the active tab handles lock or unlock.';
    }

    if (elements.folderTabPanel) {
      elements.folderTabPanel.hidden = false;
    }
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
        elements.subtitle.textContent = 'Start with a single encrypted vault. The first-vault action now lives directly inside Folder View.';
      } else if (hasVaults && !unlocked) {
        elements.subtitle.textContent = 'Choose a vault from the folder tabs, then unlock it. New vault creation stays inside Folder View so the modal reads as one surface.';
      } else {
        elements.subtitle.textContent = 'The loaded vault stays focused on folder browsing. Switch vaults from the folder tabs and use the header actions when you need a new vault or a new encrypted entry.';
      }
    }

    if (elements.loadingState) {
      elements.loadingState.hidden = !isInitialHydrate;
    }

    if (elements.openEntryBtn) {
      elements.openEntryBtn.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
    }

    if (elements.openExportBtn) {
      elements.openExportBtn.hidden = isInitialHydrate || showEmptyCreateMode || !unlocked;
      elements.openExportBtn.disabled = isInitialHydrate || !hasVaults || !activeVaultID() || !unlocked;
    }

    if (elements.mainGrid) {
      elements.mainGrid.hidden = isInitialHydrate;
    }

    if (elements.settingsLink) {
      elements.settingsLink.hidden = settingsHidden;
    }

    if (elements.footer) {
      elements.footer.classList.toggle('is-empty', settingsHidden);
    }

    syncCreateDialog();
    syncExportDialog();
    syncEntryDialog();
    syncWorkspaceTabs();
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
      const isActive = item.id === activeVaultID();
      const activeClass = isActive ? ' is-active' : '';
      const canToggleAccess = isActive && Boolean(state.status?.available);
      const locked = canToggleAccess ? Boolean(state.status?.locked) : true;
      const actionButton = canToggleAccess
        ? (
          '<button type="button" class="vault-modal-vault-pill-icon" data-action="toggle-vault-access" data-vault-id="' + escapeHTML(item.id) + '" aria-label="' + escapeHTML((locked ? 'Unlock ' : 'Lock ') + item.name) + '" title="' + escapeHTML(locked ? 'Unlock vault' : 'Lock vault') + '">' +
            vaultLockIcon(locked) +
          '</button>'
        )
        : '';

      return (
        '<div class="vault-modal-vault-pill' + activeClass + '">' +
          '<button type="button" class="vault-modal-vault-pill-main" data-action="select-vault-chip" data-vault-id="' + escapeHTML(item.id) + '">' +
            '<span class="vault-modal-vault-pill-label">' + escapeHTML(item.name) + '</span>' +
            '<span class="vault-modal-vault-pill-count">' + String(vaultRecordCount(item)) + '</span>' +
          '</button>' +
          actionButton +
        '</div>'
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

  function closeCreateDialog(options) {
    state.createDialogOpen = false;
    syncCreateDialog();
    const restoreFocus = options?.restoreFocus !== false;
    if (restoreFocus && elements?.modal?.classList.contains('show')) {
      window.requestAnimationFrame(function() {
        elements?.openCreateVaultBtn?.focus();
      });
    }
  }

  function openCreateDialog() {
    state.entryDialogOpen = false;
    syncEntryDialog();
    state.exportDialogOpen = false;
    syncExportDialog();
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    state.createDialogOpen = true;
    syncCreateDialog();
    window.requestAnimationFrame(function() {
      elements?.newVaultNameInput?.focus();
    });
  }

  function closeEntryDialog(options) {
    resetEntryDialogState();
    state.entryDialogOpen = false;
    syncEntryDialog();
    const restoreFocus = options?.restoreFocus !== false;
    if (restoreFocus && elements?.modal?.classList.contains('show')) {
      window.requestAnimationFrame(function() {
        elements?.openEntryBtn?.focus();
      });
    }
  }

  function openEntryDialog() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before saving an entry.', 'warning');
      return;
    }

    if (!state.status || !state.status.available || state.status.locked) {
      showAlert('Unlock the selected vault before creating a new entry.', 'warning');
      return;
    }

    resetEntryDialogState();
    resetCreateForm();
    state.createDialogOpen = false;
    syncCreateDialog();
    state.exportDialogOpen = false;
    syncExportDialog();
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    state.entryDialogOpen = true;
    syncEntryDialog();
    window.requestAnimationFrame(function() {
      elements?.entryLabelInput?.focus();
    });
  }

  async function openEditEntryDialog(recordID) {
    if (!recordID) {
      showAlert('Select an entry before editing it.', 'warning');
      return;
    }

    if (!activeVaultID()) {
      showAlert('Create or select a vault before editing an entry.', 'warning');
      return;
    }

    if (!state.status || !state.status.available || state.status.locked) {
      showAlert('Unlock the selected vault before editing an entry.', 'warning');
      return;
    }

    let record = state.selectedRecord && state.selectedRecord.id === recordID
      ? state.selectedRecord
      : null;

    if (!record) {
      try {
        record = await apiRequest(vaultURL('/api/vault/records/' + encodeURIComponent(recordID)));
      } catch (error) {
        console.error('Failed to load entry for editing:', error);
        showAlert(error.message || 'Failed to load entry for editing.', 'error');
        return;
      }
    }

    state.entryDialogMode = 'edit';
    state.entryDialogRecord = record;
    populateEntryForm(record);
    state.createDialogOpen = false;
    syncCreateDialog();
    state.exportDialogOpen = false;
    syncExportDialog();
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    state.entryDialogOpen = true;
    syncEntryDialog();
    window.requestAnimationFrame(function() {
      elements?.entryLabelInput?.focus();
      elements?.entryLabelInput?.select();
    });
  }

  function closeUnlockDialog(options) {
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    const restoreFocus = options?.restoreFocus !== false;
    if (restoreFocus && elements?.modal?.classList.contains('show') && state.status?.locked) {
      window.requestAnimationFrame(function() {
        const actionButton = currentVaultActionButton();
        if (actionButton) {
          actionButton.focus();
          return;
        }
        currentVaultTabButton()?.focus();
      });
    }
  }

  function closeExportDialog(options) {
    state.exportDialogOpen = false;
    syncExportDialog();
    const restoreFocus = options?.restoreFocus !== false;
    if (restoreFocus && elements?.modal?.classList.contains('show')) {
      window.requestAnimationFrame(function() {
        elements?.openExportBtn?.focus();
      });
    }
  }

  function openExportDialog() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before exporting.', 'warning');
      return;
    }

    if (!state.status || !state.status.available || state.status.locked) {
      showAlert('Unlock the selected vault before exporting it.', 'warning');
      return;
    }

    state.entryDialogOpen = false;
    syncEntryDialog();
    state.createDialogOpen = false;
    syncCreateDialog();
    state.unlockDialogOpen = false;
    syncUnlockDialog();
    state.exportDialogOpen = true;
    syncExportDialog();
    window.requestAnimationFrame(function() {
      elements?.exportPasswordInput?.focus();
    });
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

    state.entryDialogOpen = false;
    syncEntryDialog();
    state.createDialogOpen = false;
    syncCreateDialog();
    state.exportDialogOpen = false;
    syncExportDialog();
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
      closeCreateDialog({ restoreFocus: false });
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
      elements.entryContentInput,
      elements.entryAttachBtn,
      elements.entryAttachmentsInput,
      elements.entryJsonModeInput,
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

    if (elements.openEntryBtn) {
      elements.openEntryBtn.disabled = createDisabled;
    }

    if (elements.unlockBtn) {
      elements.unlockBtn.disabled = !state.status || !state.status.available || !Boolean(locked);
    }
  }

  function renderStatus(status) {
    state.status = status || null;
    if (!elements || !state.status) {
      return;
    }

    if (!state.status.available) {
      if (elements.passwordHelp) {
        elements.passwordHelp.textContent = 'Per-vault passwords are required for new vaults. Legacy vaults may still unlock through secure system storage or the older fallback passphrase flow.';
      }
      if (elements.selectedVaultState) {
        elements.selectedVaultState.textContent = 'Create a vault to begin.';
      }
      if (elements.vaultHint) {
        elements.vaultHint.textContent = 'Use the vault chip lock icon to unlock or lock the selected vault.';
      }
      closeEntryDialog({ restoreFocus: false });
      renderVaultRail();
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

    if (elements.passwordHelp) {
      elements.passwordHelp.textContent = passwordProtected
        ? 'Enter the password for the selected vault to unlock it.'
        : 'This legacy vault may still unlock through secure system storage or the older fallback passphrase flow.';
    }
    if (elements.selectedVaultState) {
      elements.selectedVaultState.textContent = locked
        ? vaultName + ' is locked. Unlock it to reveal the encrypted folder.'
        : vaultName + ' is loaded and ready for folder browsing.';
    }
    if (elements.vaultHint) {
      elements.vaultHint.textContent = locked
        ? 'Use the lock icon on the selected vault chip to unlock it.'
        : 'Use the open-lock icon on the selected vault chip to lock it again.';
    }
    renderVaultRail();

    applyModalMode();
    updateLauncherCount(state.status);
    setInteractiveState(locked);

    if (!locked) {
      closeUnlockDialog({ restoreFocus: false });
    } else {
      closeEntryDialog({ restoreFocus: false });
      syncUnlockDialog();
    }
  }

  function resetCreateForm() {
    if (!elements) {
      return;
    }

    if (isEditingEntryDialog() && state.entryDialogRecord) {
      populateEntryForm(state.entryDialogRecord);
      return;
    }

    elements.entryTypeInput.value = 'personal_note';
    elements.entryWorkspaceInput.value = '';
    elements.entryLabelInput.value = '';
    elements.entryContentInput.value = '';
    state.entryAttachments = [];
    elements.entryJsonModeInput.checked = false;
    elements.entryTagsInput.value = '';
    elements.entrySourceInput.value = '';
    elements.entryRetentionInput.value = '';
    elements.entryPayloadInput.value = defaultPayloadValue(elements.entryTypeInput.value);
    if (elements.entryAttachmentsInput) {
      elements.entryAttachmentsInput.value = '';
    }
    if (elements.entryAdvancedDetails) {
      elements.entryAdvancedDetails.open = false;
    }
    renderEntryAttachments();
    syncEntryComposerPresentation();
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

  function renderRecordAttachmentCard(attachment) {
    const normalized = normalizeEntryAttachment(attachment);
    if (!normalized) {
      return '';
    }

    const thumbnail = normalized.kind === 'image'
      ? '<img class="vault-modal-attachment-thumb" src="' + escapeHTML(attachmentDataURL(normalized)) + '" alt="' + escapeHTML(normalized.name) + '">'
      : '<div class="vault-modal-attachment-thumb is-generic">' + attachmentIcon(normalized.kind) + '</div>';

    return (
      '<div class="vault-modal-attachment-card">' +
        thumbnail +
        '<div class="vault-modal-attachment-card-body">' +
          '<div class="vault-modal-attachment-name">' + escapeHTML(normalized.name) + '</div>' +
          '<div class="vault-modal-attachment-meta">' + escapeHTML([normalized.kind === 'image' ? 'Image' : 'File', formatBytes(normalized.size_bytes)].join(' • ')) + '</div>' +
          '<button type="button" class="modern-btn modern-btn-secondary vault-modal-attachment-download" data-action="download-attachment" data-attachment-id="' + escapeHTML(normalized.id) + '">Download</button>' +
        '</div>' +
      '</div>'
    );
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

  function renderFolderVaultTabs() {
    if (!elements?.folderVaultTabs) {
      return;
    }

    if (!state.status || state.vaults.length === 0) {
      elements.folderVaultTabs.hidden = true;
      elements.folderVaultTabs.innerHTML = '';
      return;
    }

    elements.folderVaultTabs.hidden = false;
    elements.folderVaultTabs.innerHTML = state.vaults.map(function(vaultItem) {
      const isActive = vaultItem.id === activeVaultID();
      const canToggleAccess = isActive && Boolean(state.status?.available);
      const locked = canToggleAccess ? Boolean(state.status?.locked) : true;
      const actionButton = canToggleAccess
        ? (
          '<button type="button" class="vault-modal-folder-vault-tab-icon' + (locked ? ' is-locked' : ' is-unlocked') + '" data-action="toggle-vault-access" data-vault-id="' + escapeHTML(vaultItem.id) + '" aria-label="' + escapeHTML((locked ? 'Unlock ' : 'Lock ') + vaultDisplayLabel(vaultItem)) + '" title="' + escapeHTML(locked ? 'Unlock vault' : 'Lock vault') + '">' +
            vaultLockIcon(locked) +
          '</button>'
        )
        : '';

      return (
        '<div class="vault-modal-folder-vault-tab-wrap' + (isActive ? ' is-active' : '') + '">' +
          '<button type="button" class="vault-modal-folder-vault-tab' + (isActive ? ' is-active' : '') + '" role="tab" aria-selected="' + (isActive ? 'true' : 'false') + '" data-action="select-folder-vault-tab" data-vault-id="' + escapeHTML(vaultItem.id) + '">' +
            '<span class="vault-modal-folder-vault-tab-label">' + escapeHTML(vaultDisplayLabel(vaultItem)) + '</span>' +
          '</button>' +
          actionButton +
        '</div>'
      );
    }).join('');
  }

  function renderFolderBreadcrumb() {
    if (!elements?.breadcrumb) {
      return;
    }

    if (!state.status || state.status.locked) {
      elements.breadcrumb.hidden = true;
      elements.breadcrumb.innerHTML = '';
      return;
    }

    const chain = folderAncestorChain(state.selectedFolderPath).slice(1);
    if (!chain.length) {
      elements.breadcrumb.hidden = true;
      elements.breadcrumb.innerHTML = '';
      return;
    }

    elements.breadcrumb.hidden = false;
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
    const attachments = entryAttachmentsFromPayload(record.payload);
    const tags = Array.isArray(record.tags) && record.tags.length > 0
      ? record.tags.map(function(tag) {
        return '<span class="vault-modal-chip">' + escapeHTML(tag) + '</span>';
      }).join('')
      : '<span class="vault-modal-chip is-muted">No tags</span>';

    const revealLabel = state.payloadRevealed ? 'Hide Payload' : 'Reveal Payload';
    const payloadClass = 'vault-modal-payload-preview' + (state.payloadRevealed ? '' : ' is-concealed');
    const attachmentsHTML = attachments.length
      ? (
        '<div class="vault-modal-attachments-wrap">' +
          '<div class="vault-modal-payload-label">Encrypted attachments</div>' +
          (
            state.payloadRevealed
              ? '<div class="vault-modal-attachment-grid">' + attachments.map(renderRecordAttachmentCard).join('') + '</div>'
              : '<div class="vault-modal-preview-empty-inline">Reveal the payload to inspect or download attached files.</div>'
          ) +
        '</div>'
      )
      : '';

    return (
      '<div class="vault-modal-detail">' +
        '<div class="vault-modal-detail-header">' +
          '<div>' +
            '<div class="vault-modal-detail-title">' + escapeHTML(record.label || 'Untitled entry') + '</div>' +
            '<div class="vault-modal-detail-meta">' + escapeHTML([recordTypeLabel(record.type), record.workspace_id].filter(Boolean).join(' • ') || 'Vault file') + '</div>' +
          '</div>' +
          '<div class="vault-modal-detail-actions">' +
            '<button type="button" class="modern-btn modern-btn-secondary" data-action="edit-record" data-record-id="' + escapeHTML(record.id) + '">Edit Entry</button>' +
            '<button type="button" class="modern-btn modern-btn-secondary" data-action="toggle-payload">' + escapeHTML(revealLabel) + '</button>' +
            '<button type="button" class="modern-btn modern-btn-secondary vault-modal-danger-btn" data-action="delete-record" data-record-id="' + escapeHTML(record.id) + '">Delete</button>' +
          '</div>' +
        '</div>' +
        '<div class="vault-modal-chip-row">' + tags + '</div>' +
        '<div class="vault-modal-detail-grid">' +
          '<div class="vault-modal-detail-item"><span>Type</span><strong>' + escapeHTML(recordTypeLabel(record.type)) + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Workspace</span><strong>' + escapeHTML(record.workspace_id || 'Global') + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Source</span><strong>' + escapeHTML(record.source || 'Not set') + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Retention</span><strong>' + escapeHTML(record.retention_policy || 'Not set') + '</strong></div>' +
          '<div class="vault-modal-detail-item"><span>Attachments</span><strong>' + String(attachments.length) + '</strong></div>' +
          '<div class="vault-modal-detail-item vault-modal-detail-item-wide"><span>Updated</span><strong>' + escapeHTML(prettyDate(record.updated_at || record.created_at)) + '</strong></div>' +
        '</div>' +
        attachmentsHTML +
        '<div class="vault-modal-payload-wrap">' +
          '<div class="vault-modal-payload-label">Protected payload</div>' +
          '<pre class="' + payloadClass + '">' + escapeHTML(prettyPayload(payloadForPreview(record.payload))) + '</pre>' +
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
    renderFolderVaultTabs();
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

    if (state.entryDialogOpen && !elements.entryOverlay?.hidden) {
      elements.entryLabelInput?.focus();
      return;
    }

    if (state.exportDialogOpen && !elements.exportOverlay?.hidden) {
      elements.exportPasswordInput?.focus();
      return;
    }

    if (state.createDialogOpen && !elements.createOverlay?.hidden) {
      elements.newVaultNameInput?.focus();
      return;
    }

    if (state.unlockDialogOpen && !elements.unlockOverlay?.hidden) {
      elements.passwordInput?.focus();
      return;
    }

    if (!state.vaults.length) {
      elements.openCreateVaultBtn?.focus();
      return;
    }
    if (state.status && state.status.locked) {
      const actionButton = currentVaultActionButton();
      if (actionButton) {
        actionButton.focus();
        return;
      }
      currentVaultTabButton()?.focus();
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

    const actionButton = currentVaultActionButton();

    try {
      setButtonLoading(actionButton, true, 'Locking');
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
      setButtonLoading(actionButton, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  function buildEntryRequestBody() {
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
      throw new Error('Title is required before saving to the vault.');
    }

    body.payload = mergeEntryAttachments(parsePayloadInput(), state.entryAttachments);
    return body;
  }

  async function saveRecord() {
    const editing = isEditingEntryDialog();

    if (!activeVaultID()) {
      showAlert('Create or select a vault before saving an entry.', 'warning');
      return;
    }

    if (!state.status || state.status.locked) {
      showAlert(editing ? 'Unlock the vault before editing an entry.' : 'Unlock the vault before creating a new entry.', 'warning');
      return;
    }

    const recordID = state.entryDialogRecord?.id || '';
    if (editing && !recordID) {
      showAlert('Select an entry before editing it.', 'warning');
      return;
    }

    let body;
    try {
      body = buildEntryRequestBody();
    } catch (error) {
      showAlert(error.message || 'Payload must be valid JSON.', 'warning');
      return;
    }

    try {
      setButtonLoading(elements.saveBtn, true, editing ? 'Updating entry' : 'Saving entry');
      const response = await apiRequest(editing
        ? vaultURL('/api/vault/records/' + encodeURIComponent(recordID))
        : '/api/vault/records', {
        method: editing ? 'PATCH' : 'POST',
        body: body
      });

      closeEntryDialog({ restoreFocus: false });

      if (editing) {
        notify('Vault entry updated.', 'success');
        await refreshVault();
      } else {
        resetCreateForm();
        state.selectedFolderPath = ROOT_FOLDER_PATH;
        notify('Vault entry saved.', 'success');
        await refreshVault(false);

        const createdRecordID = response?.record?.id;
        if (createdRecordID) {
          await selectRecord(createdRecordID);
        }
      }
    } catch (error) {
      console.error('Failed to save vault entry:', error);
      showAlert(error.message || (editing ? 'Failed to update vault entry.' : 'Failed to save vault entry.'), 'error');
    } finally {
      setButtonLoading(elements.saveBtn, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  async function deleteRecord(recordID) {
    const targetRecord = (state.selectedRecord && state.selectedRecord.id === recordID)
      ? state.selectedRecord
      : state.recordIndex.get(recordID);

    if (!recordID || !targetRecord) {
      showAlert('Select an entry before deleting it.', 'warning');
      return;
    }

    const confirmed = window.confirm('Delete "' + (targetRecord.label || 'Untitled entry') + '" from this vault? This cannot be undone.');
    if (!confirmed) {
      return;
    }

    const deleteButton = elements.preview?.querySelector('[data-action="delete-record"][data-record-id="' + escapeSelectorValue(recordID) + '"]');

    try {
      setButtonLoading(deleteButton, true, 'Deleting');
      await apiRequest(vaultURL('/api/vault/records/' + encodeURIComponent(recordID)), {
        method: 'DELETE'
      });

      if (state.entryDialogOpen && state.entryDialogRecord?.id === recordID) {
        closeEntryDialog({ restoreFocus: false });
      }
      if (state.selectedRecord?.id === recordID) {
        clearSelection();
      }

      notify('Vault entry deleted.', 'success');
      await refreshVault(false);
    } catch (error) {
      console.error('Failed to delete vault entry:', error);
      showAlert(error.message || 'Failed to delete vault entry.', 'error');
    } finally {
      setButtonLoading(deleteButton, false);
      setInteractiveState(Boolean(state.status?.locked));
    }
  }

  async function exportVaultBundle() {
    if (!activeVaultID()) {
      showAlert('Create or select a vault before exporting.', 'warning');
      return;
    }

    if (!state.status || !state.status.available || state.status.locked) {
      showAlert('Unlock the selected vault before exporting it.', 'warning');
      return;
    }

    if (!elements?.exportConfirmInput?.checked) {
      showAlert('Confirm the export warning before generating an export.', 'warning');
      return;
    }

    const exportPassword = String(elements.exportPasswordInput?.value || '').trim();
    if (!exportPassword) {
      showAlert('Enter an export password before exporting.', 'warning');
      return;
    }

    const selectedVault = activeVault();
    const datePart = new Date().toISOString().slice(0, 10);
    const fileName = 'ori-vault-export-' + slugifyFilename(selectedVault?.name || 'vault') + '-' + datePart + '.json';

    try {
      setButtonLoading(elements.exportBtn, true, 'Exporting');
      const bundle = await apiRequest('/api/vault/export', {
        method: 'POST',
        body: {
          vault_id: activeVaultID(),
          workspace_id: String(elements.exportWorkspaceInput?.value || '').trim(),
          vault_password: exportPassword,
          confirm: true
        }
      });

      const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = fileName;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);

      notify('Vault export generated.', 'success');
      closeExportDialog({ restoreFocus: false });
    } catch (error) {
      console.error('Failed to export vault:', error);
      showAlert(error.message || 'Failed to export vault.', 'error');
    } finally {
      setButtonLoading(elements.exportBtn, false);
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
        return;
      }

      if (action === 'edit-record') {
        const recordID = target.getAttribute('data-record-id');
        if (recordID) {
          openEditEntryDialog(recordID).catch(function(error) {
            console.error('Failed to open edit entry dialog:', error);
          });
        }
        return;
      }

      if (action === 'delete-record') {
        const recordID = target.getAttribute('data-record-id');
        if (recordID) {
          deleteRecord(recordID);
        }
        return;
      }

      if (action === 'download-attachment') {
        const attachmentID = target.getAttribute('data-attachment-id');
        const attachment = entryAttachmentsFromPayload(state.selectedRecord?.payload).find(function(item) {
          return item.id === attachmentID;
        });
        if (attachment) {
          downloadAttachment(attachment);
        }
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

    elements.folderVaultTabs?.addEventListener('click', function(event) {
      const target = event.target.closest('[data-action]');
      if (!target) {
        return;
      }

      const action = target.getAttribute('data-action');
      const vaultID = target.getAttribute('data-vault-id');
      if (!vaultID) {
        return;
      }

      if (action === 'toggle-vault-access') {
        if (vaultID !== activeVaultID()) {
          switchVault(vaultID, { promptUnlock: false }).then(function() {
            if (state.status?.locked) {
              openUnlockDialog();
              return;
            }
            lockVault();
          }).catch(function(error) {
            console.error('Failed to toggle folder vault tab access:', error);
          });
          return;
        }

        if (state.status?.locked) {
          openUnlockDialog();
        } else if (state.status?.available) {
          lockVault();
        }
        return;
      }

      if (action === 'select-folder-vault-tab') {
        switchVault(vaultID, { promptUnlock: true }).catch(function(error) {
          console.error('Failed to switch vault from folder tabs:', error);
        });
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

    elements.openCreateVaultBtn?.addEventListener('click', function() {
      openCreateDialog();
    });

    elements.openExportBtn?.addEventListener('click', function() {
      openExportDialog();
    });

    elements.openEntryBtn?.addEventListener('click', function() {
      openEntryDialog();
    });

    elements.unlockBtn?.addEventListener('click', function() {
      unlockVault();
    });

    elements.createCancelBtn?.addEventListener('click', function() {
      closeCreateDialog();
    });

    elements.entryCancelBtn?.addEventListener('click', function() {
      closeEntryDialog();
    });

    elements.exportCancelBtn?.addEventListener('click', function() {
      closeExportDialog();
    });

    elements.unlockCancelBtn?.addEventListener('click', function() {
      closeUnlockDialog();
    });

    elements.createOverlay?.addEventListener('click', function(event) {
      const dismissTrigger = event.target.closest('[data-action="dismiss-create-dialog"]');
      if (dismissTrigger) {
        closeCreateDialog();
      }
    });

    elements.entryOverlay?.addEventListener('click', function(event) {
      const dismissTrigger = event.target.closest('[data-action="dismiss-entry-dialog"]');
      if (dismissTrigger) {
        closeEntryDialog();
      }
    });

    elements.exportOverlay?.addEventListener('click', function(event) {
      const dismissTrigger = event.target.closest('[data-action="dismiss-export-dialog"]');
      if (dismissTrigger) {
        closeExportDialog();
      }
    });

    elements.unlockOverlay?.addEventListener('click', function(event) {
      const dismissTrigger = event.target.closest('[data-action="dismiss-unlock-dialog"]');
      if (dismissTrigger) {
        closeUnlockDialog();
      }
    });

    elements.vaultSelect?.addEventListener('change', function(event) {
      switchVault(event.target.value, { promptUnlock: true });
    });

    elements.vaultRail?.addEventListener('click', function(event) {
      const trigger = event.target.closest('[data-action]');
      if (!trigger) {
        return;
      }

      const action = trigger.getAttribute('data-action');
      const vaultID = trigger.getAttribute('data-vault-id');
      if (!vaultID) {
        return;
      }

      if (action === 'toggle-vault-access') {
        if (vaultID !== activeVaultID()) {
          switchVault(vaultID, { promptUnlock: false }).then(function() {
            if (state.status?.locked) {
              openUnlockDialog();
              return;
            }
            lockVault();
          }).catch(function(error) {
            console.error('Failed to switch vault before toggling access:', error);
          });
          return;
        }

        if (state.status?.locked) {
          openUnlockDialog();
        } else if (state.status?.available) {
          lockVault();
        }
        return;
      }

      if (action === 'select-vault-chip') {
        switchVault(vaultID, { promptUnlock: true });
      }
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

    elements.exportPasswordInput?.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        exportVaultBundle();
      }
    });

    elements.entryTypeInput?.addEventListener('change', function() {
      syncEntryComposerPresentation();
    });

    elements.entryJsonModeInput?.addEventListener('change', function() {
      syncEntryComposerPresentation();
    });

    elements.entryAttachBtn?.addEventListener('click', function() {
      elements.entryAttachmentsInput?.click();
    });

    elements.entryAttachmentsInput?.addEventListener('change', function(event) {
      addEntryAttachments(event.target.files).catch(function(error) {
        console.error('Failed to add vault attachments:', error);
        showAlert(error.message || 'Failed to add attachment.', 'error');
      });
    });

    elements.entryAttachmentsList?.addEventListener('click', function(event) {
      const target = event.target.closest('[data-action="remove-entry-attachment"]');
      if (!target) {
        return;
      }

      const attachmentID = target.getAttribute('data-attachment-id');
      if (attachmentID) {
        removeEntryAttachment(attachmentID);
      }
    });

    elements.resetBtn?.addEventListener('click', function() {
      resetCreateForm();
      showAlert('');
    });

    elements.saveBtn?.addEventListener('click', function() {
      saveRecord();
    });

    elements.exportBtn?.addEventListener('click', function() {
      exportVaultBundle();
    });

    elements.searchInput?.addEventListener('input', function() {
      renderExplorerPreview();
    });

    bindFolderTreeEvents();
    bindPreviewEvents();
    bindBreadcrumbEvents();

    elements.modal?.addEventListener('keydown', function(event) {
      if (event.key === 'Escape' && state.entryDialogOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeEntryDialog();
        return;
      }
      if (event.key === 'Escape' && state.exportDialogOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeExportDialog();
        return;
      }
      if (event.key === 'Escape' && state.createDialogOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeCreateDialog();
        return;
      }
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
        closeEntryDialog({ restoreFocus: false });
        closeExportDialog({ restoreFocus: false });
        closeCreateDialog({ restoreFocus: false });
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

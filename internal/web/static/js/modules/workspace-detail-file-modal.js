/**
 * Workspace File Modal Manager
 *
 * Owns the "Add to Workspace" file modal: device-file vs vault-attachment
 * source toggle, drag-and-drop and file-input handling for device uploads,
 * vault selector + search + attachment grid, vault unlock flow, selection
 * preview, and the upload submit path that POSTs each selected item to
 * /api/workspaces/{id}/files (with vault references when applicable).
 *
 * Extracted from workspace-detail.js. Instantiated by WorkspaceDetailPage,
 * which provides workspaceId, escapeHtml, formatFileSize, loadFiles, and
 * the vault helpers (buildVaultReferenceFromItem, vaultReferenceNotes,
 * buildWorkspaceFileFromVaultAttachment) through the host. The vault
 * panel-drop handlers and the broader vault helper set stay on the host
 * since they're used widely outside the file modal.
 *
 * @module workspace-detail-file-modal
 */

export class WorkspaceFileModalManager {
  constructor(host) {
    this.host = host;
    this.fileModalElements = {};
    this.fileModalState = {
      source: 'device',
      selectedItems: [],
      vaults: [],
      selectedVaultId: '',
      vaultStatus: null,
      vaultAttachments: [],
      searchQuery: '',
      loadingVaults: false,
      loadingAttachments: false
    };
  }

  setupFileModal() {
    const modal = document.getElementById('hubAddFileModal');
    const dropZone = document.getElementById('hubFileDropZone');
    const fileInput = document.getElementById('hubFileInput');
    const submitBtn = document.getElementById('hubAddFileSubmitBtn');
    const selectedFilesPreview = document.getElementById('hubSelectedFilesPreview');
    const selectedFilesList = document.getElementById('hubSelectedFilesList');
    const titleInput = document.getElementById('hubFileTitle');
    const notesInput = document.getElementById('hubFileNotes');
    const folderSelect = document.getElementById('hubFileFolderSelect');
    const folderPath = document.getElementById('hubFileFolderPath');
    const createFolderBtn = document.getElementById('hubCreateUploadFolderBtn');
    const devicePane = document.getElementById('hubFileDevicePane');
    const vaultPane = document.getElementById('hubFileVaultPane');
    const deviceBtn = document.getElementById('hubFileSourceDeviceBtn');
    const vaultBtn = document.getElementById('hubFileSourceVaultBtn');
    const vaultSelect = document.getElementById('hubVaultSelect');
    const vaultRefreshBtn = document.getElementById('hubVaultRefreshBtn');
    const vaultSearchInput = document.getElementById('hubVaultSearchInput');
    const vaultLockedState = document.getElementById('hubVaultLockedState');
    const vaultUnlockPassword = document.getElementById('hubVaultUnlockPassword');
    const vaultUnlockBtn = document.getElementById('hubVaultUnlockBtn');
    const vaultAttachmentList = document.getElementById('hubVaultAttachmentList');

    if (
      !modal ||
      !dropZone ||
      !fileInput ||
      !submitBtn ||
      !selectedFilesPreview ||
      !selectedFilesList ||
      !devicePane ||
      !vaultPane
    ) {
      return;
    }

    this.fileModalElements = {
      modal,
      dropZone,
      fileInput,
      submitBtn,
      selectedFilesPreview,
      selectedFilesList,
      titleInput,
      notesInput,
      folderSelect,
      folderPath,
      createFolderBtn,
      devicePane,
      vaultPane,
      deviceBtn,
      vaultBtn,
      vaultSelect,
      vaultRefreshBtn,
      vaultSearchInput,
      vaultLockedState,
      vaultUnlockPassword,
      vaultUnlockBtn,
      vaultAttachmentList
    };

    deviceBtn?.addEventListener('click', () => {
      this.setFileModalSource('device');
    });

    vaultBtn?.addEventListener('click', async () => {
      await this.setFileModalSource('vault');
    });

    dropZone.addEventListener('click', () => {
      if (this.fileModalState.source === 'device') {
        fileInput.click();
      }
    });

    dropZone.addEventListener('dragover', event => {
      if (this.fileModalState.source !== 'device') {
        return;
      }
      event.preventDefault();
      dropZone.classList.add('drag-active');
    });

    dropZone.addEventListener('dragleave', () => {
      dropZone.classList.remove('drag-active');
    });

    dropZone.addEventListener('drop', event => {
      if (this.fileModalState.source !== 'device') {
        return;
      }
      event.preventDefault();
      dropZone.classList.remove('drag-active');
      if (event.dataTransfer.files.length > 0) {
        this.fileModalState.selectedItems = Array.from(event.dataTransfer.files).map(file => ({
          source: 'device',
          name: file.name,
          size: Number(file.size || 0),
          file
        }));
        this.renderFileModalSelectionPreview();
      }
    });

    fileInput.addEventListener('change', () => {
      if (fileInput.files.length > 0) {
        this.fileModalState.selectedItems = Array.from(fileInput.files).map(file => ({
          source: 'device',
          name: file.name,
          size: Number(file.size || 0),
          file
        }));
        this.renderFileModalSelectionPreview();
      }
    });

    vaultSelect?.addEventListener('change', async () => {
      this.fileModalState.selectedVaultId = String(vaultSelect.value || '').trim();
      this.fileModalState.selectedItems = [];
      this.renderFileModalSelectionPreview();
      await this.loadFileModalVaultAttachments();
    });

    vaultSearchInput?.addEventListener('input', () => {
      this.fileModalState.searchQuery = String(vaultSearchInput.value || '')
        .trim()
        .toLowerCase();
      this.renderFileModalVaultAttachments();
    });

    vaultRefreshBtn?.addEventListener('click', async () => {
      await this.loadFileModalVaults(true);
    });

    vaultUnlockBtn?.addEventListener('click', async () => {
      await this.unlockSelectedVaultForFileModal();
    });

    vaultUnlockPassword?.addEventListener('keydown', async event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        await this.unlockSelectedVaultForFileModal();
      }
    });

    folderSelect?.addEventListener('change', () => {
      this.setFileModalFolderPath(folderSelect.value);
    });

    createFolderBtn?.addEventListener('click', async () => {
      await this.createFileModalFolder();
    });

    vaultAttachmentList?.addEventListener('click', event => {
      const trigger = event.target.closest('[data-vault-attachment-id]');
      if (!trigger) {
        return;
      }

      const attachmentId = String(trigger.getAttribute('data-vault-attachment-id') || '').trim();
      if (attachmentId) {
        this.toggleVaultAttachmentSelection(attachmentId);
      }
    });

    submitBtn.addEventListener('click', async () => {
      await this.uploadSelectedFileModalItems();
    });

    modal.addEventListener('show.bs.modal', async () => {
      this.resetFileModalState();
      this.renderFileModalSource();
      this.renderFileModalSelectionPreview();
      await this.loadFileModalFolderOptions('');
    });

    modal.addEventListener('hidden.bs.modal', () => {
      this.resetFileModalState();
    });
  }

  resetFileModalState() {
    this.fileModalState = {
      source: 'device',
      selectedItems: [],
      vaults: [],
      selectedVaultId: '',
      vaultStatus: null,
      vaultAttachments: [],
      searchQuery: '',
      loadingVaults: false,
      loadingAttachments: false
    };

    const {
      fileInput,
      titleInput,
      notesInput,
      folderSelect,
      folderPath,
      vaultSelect,
      vaultSearchInput,
      vaultUnlockPassword
    } = this.fileModalElements;

    if (fileInput) {
      fileInput.value = '';
    }
    if (titleInput) {
      titleInput.value = '';
    }
    if (notesInput) {
      notesInput.value = '';
    }
    if (folderPath) {
      folderPath.value = '';
    }
    if (folderSelect) {
      folderSelect.innerHTML = '<option value="">Workspace files</option>';
      folderSelect.value = '';
      folderSelect.disabled = false;
    }
    if (vaultSelect) {
      vaultSelect.innerHTML = '<option value="">Loading vaults...</option>';
    }
    if (vaultSearchInput) {
      vaultSearchInput.value = '';
      vaultSearchInput.disabled = false;
    }
    if (vaultUnlockPassword) {
      vaultUnlockPassword.value = '';
    }

    this.renderFileModalSource();
    this.renderFileModalSelectionPreview();
    this.renderFileModalVaultAttachments();
  }

  normalizeFileModalFolderPath(value) {
    return String(value || '')
      .trim()
      .replace(/\\/g, '/')
      .replace(/^\.\/+/, '')
      .replace(/^\/+/, '')
      .replace(/\/+/g, '/')
      .replace(/\/+$/, '');
  }

  setFileModalFolderPath(value) {
    const normalizedPath = this.normalizeFileModalFolderPath(value);
    const { folderPath, folderSelect } = this.fileModalElements;
    if (folderPath) {
      folderPath.value = normalizedPath;
    }
    if (folderSelect) {
      folderSelect.value = normalizedPath;
    }
    return normalizedPath;
  }

  async loadFileModalFolderOptions(selectedPath = '') {
    const { folderSelect } = this.fileModalElements;
    if (!folderSelect || !this.host.workspaceId) {
      return;
    }

    const normalizedSelected = this.normalizeFileModalFolderPath(selectedPath);
    folderSelect.disabled = true;
    folderSelect.innerHTML = '<option value="">Workspace files</option>';

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/files/tree`
      );
      if (!response.ok) {
        throw new Error('Failed to load workspace folders');
      }

      const payload = await response.json();
      const folders = (Array.isArray(payload.files) ? payload.files : [])
        .filter(item => item?.is_dir && item?.relative_path)
        .sort((left, right) =>
          String(left.relative_path || '').localeCompare(
            String(right.relative_path || ''),
            undefined,
            {
              sensitivity: 'base',
              numeric: true
            }
          )
        );

      folderSelect.innerHTML = [
        '<option value="">Workspace files</option>',
        ...folders.map(folder => {
          const path = this.normalizeFileModalFolderPath(folder.relative_path);
          return `<option value="${this.host.escapeHtml(path)}">${this.host.escapeHtml(path)}</option>`;
        })
      ].join('');

      const hasSelectedOption = Array.from(folderSelect.options).some(
        option => option.value === normalizedSelected
      );
      this.setFileModalFolderPath(hasSelectedOption ? normalizedSelected : '');
    } catch (error) {
      console.error('Failed to load file modal folder options:', error);
      this.setFileModalFolderPath('');
    } finally {
      folderSelect.disabled = false;
    }
  }

  async createFileModalFolder() {
    if (!this.host.workspaceId) {
      return;
    }

    const currentPath = this.normalizeFileModalFolderPath(
      this.fileModalElements.folderPath?.value || this.fileModalElements.folderSelect?.value || ''
    );
    const defaultPath = currentPath ? `${currentPath}/New Folder` : 'New Folder';
    const rawPath = window.prompt('New folder path inside Workspace files', defaultPath);
    if (rawPath === null) return;

    const folderPath = this.normalizeFileModalFolderPath(rawPath);
    if (!folderPath) return;

    const { createFolderBtn } = this.fileModalElements;
    const originalText = createFolderBtn?.textContent || '';
    if (createFolderBtn) {
      createFolderBtn.disabled = true;
      createFolderBtn.textContent = 'Creating...';
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/folders`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: folderPath })
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload.error || payload.message || 'Failed to create folder');
      }

      const createdPath = this.normalizeFileModalFolderPath(payload?.folder?.path || folderPath);
      await this.loadFileModalFolderOptions(createdPath);
      if (window.Toast) window.Toast.success('Folder created');
      if (typeof this.host.loadFiles === 'function') {
        await this.host.loadFiles();
      }
    } catch (error) {
      console.error('Failed to create file modal folder:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to create folder');
      }
    } finally {
      if (createFolderBtn) {
        createFolderBtn.disabled = false;
        createFolderBtn.textContent = originalText || 'New Folder';
      }
    }
  }

  async setFileModalSource(source) {
    const normalized = source === 'vault' ? 'vault' : 'device';
    if (this.fileModalState.source !== normalized) {
      this.fileModalState.source = normalized;
      this.fileModalState.selectedItems = [];
      if (this.fileModalElements.fileInput) {
        this.fileModalElements.fileInput.value = '';
      }
    }

    this.renderFileModalSource();
    this.renderFileModalSelectionPreview();

    if (normalized === 'vault') {
      await this.loadFileModalVaults(false);
    }
  }

  renderFileModalSource() {
    const { devicePane, vaultPane, deviceBtn, vaultBtn } = this.fileModalElements;

    const usingVault = this.fileModalState.source === 'vault';

    if (devicePane) {
      devicePane.hidden = usingVault;
    }
    if (vaultPane) {
      vaultPane.hidden = !usingVault;
    }

    if (deviceBtn) {
      deviceBtn.classList.toggle('is-active', !usingVault);
      deviceBtn.setAttribute('aria-pressed', usingVault ? 'false' : 'true');
    }
    if (vaultBtn) {
      vaultBtn.classList.toggle('is-active', usingVault);
      vaultBtn.setAttribute('aria-pressed', usingVault ? 'true' : 'false');
    }
  }

  renderFileModalSelectionPreview() {
    const { selectedFilesPreview, selectedFilesList, submitBtn } = this.fileModalElements;
    const items = Array.isArray(this.fileModalState.selectedItems)
      ? this.fileModalState.selectedItems
      : [];

    if (!selectedFilesPreview || !selectedFilesList || !submitBtn) {
      return;
    }

    if (!items.length) {
      selectedFilesPreview.style.display = 'none';
      submitBtn.disabled = true;
      return;
    }

    selectedFilesPreview.style.display = 'block';
    submitBtn.disabled = false;

    selectedFilesList.innerHTML = items
      .map((item, index) => {
        const meta = [];
        if (Number.isFinite(item.size) && item.size > 0) {
          meta.push(this.host.formatFileSize(item.size));
        }
        if (item.source === 'vault') {
          meta.push(item.recordLabel || 'Vault entry');
        }

        return `
        <div class="d-flex justify-content-between align-items-center p-2" style="background: var(--bg-secondary); border-radius: 8px; gap: 0.75rem;">
          <div style="min-width: 0;">
            <div style="color: var(--text-primary); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
              ${this.host.escapeHtml(item.name || 'Untitled file')}
              <span class="hub-selected-files-source">${item.source === 'vault' ? 'Vault' : 'Device'}</span>
            </div>
            <div style="color: var(--text-secondary); font-size: 0.76rem; margin-top: 2px;">
              ${this.host.escapeHtml(meta.join(' • '))}
            </div>
          </div>
          <button type="button" class="btn-close btn-sm" onclick="window.workspaceDetail?.removeFileModalSelection(${index})"></button>
        </div>
      `;
      })
      .join('');
  }

  removeFileModalSelection(index) {
    if (!Array.isArray(this.fileModalState.selectedItems)) {
      return;
    }

    this.fileModalState.selectedItems.splice(index, 1);
    this.renderFileModalSelectionPreview();
    if (this.fileModalState.source === 'vault') {
      this.renderFileModalVaultAttachments();
    }
  }

  async loadFileModalVaults(force = false) {
    const { vaultSelect } = this.fileModalElements;
    if (this.fileModalState.loadingVaults) {
      return;
    }

    if (!force && this.fileModalState.vaults.length) {
      if (!this.fileModalState.selectedVaultId) {
        const storedVaultId = String(
          window.localStorage?.getItem('ori-selected-vault-id') || ''
        ).trim();
        this.fileModalState.selectedVaultId =
          this.fileModalState.vaults.find(item => item.id === storedVaultId)?.id ||
          this.fileModalState.vaults[0]?.id ||
          '';
        if (vaultSelect) {
          vaultSelect.value = this.fileModalState.selectedVaultId;
        }
      }
      await this.loadFileModalVaultAttachments();
      return;
    }

    this.fileModalState.loadingVaults = true;
    if (vaultSelect) {
      vaultSelect.innerHTML = '<option value="">Loading vaults...</option>';
      vaultSelect.disabled = true;
    }
    this.renderFileModalVaultAttachments();

    try {
      const response = await fetch('/api/vault/vaults');
      if (!response.ok) {
        throw new Error('Failed to load vaults');
      }

      const data = await response.json();
      this.fileModalState.vaults = Array.isArray(data?.vaults) ? data.vaults : [];

      const storedVaultId = String(
        window.localStorage?.getItem('ori-selected-vault-id') || ''
      ).trim();
      const preferredVaultId = this.fileModalState.selectedVaultId || storedVaultId;
      this.fileModalState.selectedVaultId =
        this.fileModalState.vaults.find(item => item.id === preferredVaultId)?.id ||
        this.fileModalState.vaults[0]?.id ||
        '';

      if (vaultSelect) {
        if (!this.fileModalState.vaults.length) {
          vaultSelect.innerHTML = '<option value="">No vaults available</option>';
        } else {
          vaultSelect.innerHTML = this.fileModalState.vaults
            .map(vault => {
              const label = `${vault.name || 'Vault'} · ${Number(vault.record_count || 0)} ${Number(vault.record_count || 0) === 1 ? 'entry' : 'entries'}`;
              const selected = vault.id === this.fileModalState.selectedVaultId ? ' selected' : '';
              return `<option value="${this.host.escapeHtml(vault.id)}"${selected}>${this.host.escapeHtml(label)}</option>`;
            })
            .join('');
        }
        vaultSelect.disabled = !this.fileModalState.vaults.length;
      }

      this.fileModalState.loadingVaults = false;
      await this.loadFileModalVaultAttachments();
    } catch (error) {
      console.error('Failed to load vaults for file modal:', error);
      this.fileModalState.vaults = [];
      this.fileModalState.selectedVaultId = '';
      this.fileModalState.vaultStatus = null;
      this.fileModalState.vaultAttachments = [];
      if (vaultSelect) {
        vaultSelect.innerHTML = '<option value="">No vaults available</option>';
        vaultSelect.disabled = true;
      }
      this.renderFileModalVaultAttachments(
        'No vaults available yet. Create and unlock a vault first.'
      );
    } finally {
      this.fileModalState.loadingVaults = false;
    }
  }

  async loadFileModalVaultAttachments() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    const { vaultUnlockPassword } = this.fileModalElements;

    this.fileModalState.vaultAttachments = [];
    this.fileModalState.vaultStatus = null;

    if (vaultUnlockPassword) {
      vaultUnlockPassword.value = '';
    }

    if (!selectedVaultId) {
      this.renderFileModalVaultAttachments();
      return;
    }

    this.fileModalState.loadingAttachments = true;
    this.renderFileModalVaultAttachments();

    try {
      const statusResponse = await fetch(
        `/api/vault/status?vault_id=${encodeURIComponent(selectedVaultId)}`
      );
      if (!statusResponse.ok) {
        throw new Error('Failed to load vault status');
      }

      const status = await statusResponse.json();
      this.fileModalState.vaultStatus = status;

      if (!status?.available || status?.locked) {
        this.renderFileModalVaultAttachments();
        return;
      }

      const listResponse = await fetch(
        `/api/vault/records?vault_id=${encodeURIComponent(selectedVaultId)}`
      );
      if (!listResponse.ok) {
        throw new Error('Failed to load vault records');
      }

      const listData = await listResponse.json();
      const records = Array.isArray(listData?.records) ? listData.records : [];
      const selectedVault = this.getFileModalSelectedVault();
      const recordDetails = await Promise.all(
        records.map(async record => {
          try {
            const response = await fetch(
              `/api/vault/records/${encodeURIComponent(record.id)}?vault_id=${encodeURIComponent(selectedVaultId)}`
            );
            if (!response.ok) {
              return null;
            }
            return await response.json();
          } catch (error) {
            console.warn('Failed to load vault record for workspace import:', record?.id, error);
            return null;
          }
        })
      );

      this.fileModalState.vaultAttachments = recordDetails
        .filter(Boolean)
        .flatMap(record =>
          this.extractVaultAttachmentsForWorkspaceModal(record, selectedVault?.name || 'Vault')
        )
        .sort((left, right) => new Date(right.updatedAt || 0) - new Date(left.updatedAt || 0));
    } catch (error) {
      console.error('Failed to load vault attachments for workspace modal:', error);
      this.fileModalState.vaultAttachments = [];
      this.renderFileModalVaultAttachments('Failed to load files from the selected vault.');
      return;
    } finally {
      this.fileModalState.loadingAttachments = false;
    }

    this.renderFileModalVaultAttachments();
  }

  getFileModalSelectedVault() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    return this.fileModalState.vaults.find(vault => vault.id === selectedVaultId) || null;
  }

  extractVaultAttachmentsForWorkspaceModal(record, vaultName) {
    const payload = record?.payload;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return [];
    }

    const attachments = Array.isArray(payload.attachments) ? payload.attachments : [];
    return attachments
      .filter(attachment => attachment && String(attachment.content_base64 || '').trim())
      .map((attachment, index) => ({
        source: 'vault',
        id: `${record.id}:${attachment.id || index}:${attachment.name || 'attachment'}`,
        attachmentId: String(attachment.id || ''),
        name: String(attachment.name || `attachment-${index + 1}`),
        size: Number(attachment.size_bytes || 0),
        mimeType: String(attachment.mime_type || 'application/octet-stream'),
        kind: String(attachment.kind || ''),
        contentBase64: String(attachment.content_base64 || ''),
        recordId: String(record.id || ''),
        recordLabel: String(record.label || 'Untitled vault entry'),
        recordType: String(record.type || ''),
        workspaceId: String(record.workspace_id || ''),
        vaultId: String(record.vault_id || this.fileModalState.selectedVaultId || ''),
        vaultName: String(vaultName || 'Vault'),
        updatedAt: record.updated_at || record.created_at || ''
      }));
  }

  renderFileModalVaultAttachments(overrideMessage = '') {
    const { vaultAttachmentList, vaultLockedState, vaultUnlockBtn, vaultSearchInput } =
      this.fileModalElements;
    if (!vaultAttachmentList || !vaultLockedState) {
      return;
    }

    const state = this.fileModalState;
    const selectedIds = new Set((state.selectedItems || []).map(item => item.id));

    if (vaultUnlockBtn) {
      vaultUnlockBtn.disabled = !state.selectedVaultId;
    }

    if (vaultSearchInput) {
      vaultSearchInput.disabled =
        state.loadingVaults ||
        state.loadingAttachments ||
        !state.selectedVaultId ||
        Boolean(state.vaultStatus?.locked);
    }

    vaultLockedState.hidden = true;

    if (overrideMessage) {
      vaultAttachmentList.innerHTML = `<div class="hub-file-vault-empty">${this.host.escapeHtml(overrideMessage)}</div>`;
      return;
    }

    if (state.loadingVaults) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">Loading vaults...</div>';
      return;
    }

    if (!state.vaults.length) {
      vaultAttachmentList.innerHTML =
        '<div class="hub-file-vault-empty">No vaults available yet. Create one in Vault first.</div>';
      return;
    }

    if (state.loadingAttachments) {
      vaultAttachmentList.innerHTML =
        '<div class="hub-file-vault-empty">Loading files from the selected vault...</div>';
      return;
    }

    if (!state.selectedVaultId) {
      vaultAttachmentList.innerHTML =
        '<div class="hub-file-vault-empty">Select a vault to browse its attached files.</div>';
      return;
    }

    if (!state.vaultStatus?.available) {
      vaultAttachmentList.innerHTML =
        '<div class="hub-file-vault-empty">That vault is not available right now.</div>';
      return;
    }

    if (state.vaultStatus?.locked) {
      vaultLockedState.hidden = false;
      if (vaultSearchInput) {
        vaultSearchInput.disabled = true;
      }
      vaultAttachmentList.innerHTML =
        '<div class="hub-file-vault-empty">Unlock the selected vault to browse attached files.</div>';
      return;
    }

    const query = String(state.searchQuery || '').trim();
    const filteredAttachments = state.vaultAttachments.filter(item => {
      if (!query) {
        return true;
      }

      const haystack = [item.name, item.recordLabel, item.workspaceId, item.vaultName]
        .join(' ')
        .toLowerCase();
      return haystack.includes(query);
    });

    if (!filteredAttachments.length) {
      vaultAttachmentList.innerHTML = state.vaultAttachments.length
        ? '<div class="hub-file-vault-empty">No vault files match this search.</div>'
        : '<div class="hub-file-vault-empty">No attached files were found in this vault yet. Add files to a vault entry first.</div>';
      return;
    }

    vaultAttachmentList.innerHTML = filteredAttachments
      .map(item => {
        const isSelected = selectedIds.has(item.id);
        const workspaceMeta = item.workspaceId ? `Workspace ${item.workspaceId}` : 'Global entry';
        return `
        <button type="button" class="hub-file-vault-item${isSelected ? ' is-selected' : ''}" data-vault-attachment-id="${this.host.escapeHtml(item.id)}">
          <span class="hub-file-vault-item-icon">${this.renderFileModalVaultIcon(item)}</span>
          <span class="hub-file-vault-item-main">
            <span class="hub-file-vault-item-name">${this.host.escapeHtml(item.name)}</span>
            <span class="hub-file-vault-item-meta">${this.host.escapeHtml(`${item.recordLabel} • ${this.host.formatFileSize(item.size || 0)}`)}</span>
            <span class="hub-file-vault-item-detail">${this.host.escapeHtml(`${item.vaultName} • ${workspaceMeta}`)}</span>
          </span>
          <span class="hub-file-vault-item-check" aria-hidden="true"></span>
        </button>
      `;
      })
      .join('');
  }

  renderFileModalVaultIcon(item) {
    const mimeType = String(item?.mimeType || '');
    const kind = String(item?.kind || '');

    if (kind === 'image' || mimeType.startsWith('image/')) {
      return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M21,19V5A2,2 0 0,0 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19M8.5,11A1.5,1.5 0 0,1 10,12.5A1.5,1.5 0 0,1 8.5,14A1.5,1.5 0 0,1 7,12.5A1.5,1.5 0 0,1 8.5,11M5,19L8.5,14.5L11,17.5L14.5,13L19,19H5Z"/></svg>';
    }

    return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';
  }

  toggleVaultAttachmentSelection(attachmentId) {
    const index = this.fileModalState.selectedItems.findIndex(item => item.id === attachmentId);
    if (index >= 0) {
      this.fileModalState.selectedItems.splice(index, 1);
    } else {
      const attachment = this.fileModalState.vaultAttachments.find(
        item => item.id === attachmentId
      );
      if (!attachment) {
        return;
      }
      this.fileModalState.selectedItems.push({ ...attachment });
    }

    this.renderFileModalSelectionPreview();
    this.renderFileModalVaultAttachments();
  }

  async unlockSelectedVaultForFileModal() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    const { vaultUnlockPassword, vaultUnlockBtn } = this.fileModalElements;
    const password = String(vaultUnlockPassword?.value || '').trim();

    if (!selectedVaultId) {
      if (window.Toast) window.Toast.error('Select a vault first');
      return;
    }

    if (!password) {
      if (window.Toast) window.Toast.error('Enter the vault password first');
      return;
    }

    if (vaultUnlockBtn) {
      vaultUnlockBtn.disabled = true;
      vaultUnlockBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-1"></span> Unlocking';
    }

    try {
      const response = await fetch(
        `/api/vault/unlock?vault_id=${encodeURIComponent(selectedVaultId)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            vault_id: selectedVaultId,
            vault_password: password
          })
        }
      );

      const contentType = response.headers.get('content-type') || '';
      const data = contentType.includes('application/json') ? await response.json() : null;
      if (!response.ok) {
        throw new Error(data?.error || 'Failed to unlock vault');
      }

      if (vaultUnlockPassword) {
        vaultUnlockPassword.value = '';
      }

      if (window.Toast) window.Toast.success('Vault unlocked');
      await this.loadFileModalVaultAttachments();
    } catch (error) {
      console.error('Failed to unlock vault for workspace file modal:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to unlock vault');
    } finally {
      if (vaultUnlockBtn) {
        vaultUnlockBtn.disabled = false;
        vaultUnlockBtn.textContent = 'Unlock';
      }
    }
  }
  async uploadSelectedFileModalItems() {
    const { submitBtn, titleInput, notesInput } = this.fileModalElements;
    const items = Array.isArray(this.fileModalState.selectedItems)
      ? this.fileModalState.selectedItems
      : [];

    if (!items.length) {
      return;
    }

    const title = String(titleInput?.value || '').trim();
    const notes = String(notesInput?.value || '').trim();

    if (submitBtn) {
      submitBtn.disabled = true;
      submitBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Adding...';
    }

    try {
      for (const item of items) {
        const file =
          item.source === 'vault'
            ? this.host.buildWorkspaceFileFromVaultAttachment(item)
            : item.file;

        if (!file) {
          throw new Error(`Missing file data for ${item.name || 'selected item'}`);
        }

        const formData = new FormData();
        formData.append('file', file);
        formData.append('workspace_id', this.host.workspaceId);
        const folderPath =
          typeof this.host.getSelectedUploadFolderPath === 'function'
            ? this.host.getSelectedUploadFolderPath()
            : '';
        if (folderPath) formData.append('folder_path', folderPath);
        if (title) formData.append('title', title);
        const vaultReference =
          item.source === 'vault' ? this.host.buildVaultReferenceFromItem(item, 'file') : null;
        if (vaultReference) {
          formData.append('vault_reference', JSON.stringify(vaultReference));
          formData.append('notes', notes || this.host.vaultReferenceNotes(vaultReference));
        } else if (notes) {
          formData.append('notes', notes);
        }

        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/files`,
          {
            method: 'POST',
            body: formData
          }
        );

        if (!response.ok) {
          throw new Error(`Failed to add ${item.name || 'file'} to the workspace`);
        }
      }

      if (window.Toast) {
        window.Toast.success(
          items.length === 1 ? 'File added to workspace' : 'Files added to workspace'
        );
      }
      await this.host.loadFiles();

      const modal =
        typeof bootstrap !== 'undefined'
          ? bootstrap.Modal.getInstance(this.fileModalElements.modal)
          : null;
      modal?.hide();
    } catch (error) {
      console.error('Workspace file modal upload failed:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to add file to workspace');
      }
    } finally {
      if (submitBtn) {
        submitBtn.disabled = this.fileModalState.selectedItems.length === 0;
        submitBtn.innerHTML =
          '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/></svg> Add to Workspace';
      }
    }
  }
}

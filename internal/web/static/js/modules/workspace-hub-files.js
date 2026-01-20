/**
 * Workspace Hub Files Management
 * Handles file uploads, file list rendering, and directory references.
 *
 * @module workspace-hub-files
 */
(function() {
  'use strict';

  const { formatFileSize, getFileIcon } = window.WorkspaceHubUtils;

  /**
   * Move a file to trash
   * @param {string} fileId - File ID to trash
   */
  async function moveFileToTrash(fileId) {
    const state = window.WorkspaceHubState.getState();

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: 'Move to Trash',
      message: 'Move this file to trash? You can restore it later from the canvas.',
      variant: 'trash'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments/${encodeURIComponent(fileId)}/trash`, {
        method: 'PATCH'
      });
      if (!response.ok) throw new Error('Failed to move file to trash');

      if (window.Toast) window.Toast.success('File moved to trash');
      await loadFiles(state.selectedId);
    } catch (error) {
      console.error('Failed to move file to trash:', error);
      if (window.Toast) window.Toast.error('Failed to move file to trash');
    }
  }

  /**
   * Bulk move files to trash
   */
  async function bulkMoveFilesToTrash() {
    const state = window.WorkspaceHubState.getState();
    const ids = Array.from(state.selectedItems.files);
    if (ids.length === 0) return;

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: `Move ${ids.length} File${ids.length > 1 ? 's' : ''} to Trash`,
      message: `Move ${ids.length} file${ids.length > 1 ? 's' : ''} to trash? You can restore them later from the canvas.`,
      variant: 'trash'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments/bulk-trash`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ attachment_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to move files to trash');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Moved ${result.success_count} file${result.success_count !== 1 ? 's' : ''} to trash`);
      }

      window.WorkspaceHubSelection.toggleSelectionMode('files', () => renderFiles(state.files));
      await loadFiles(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk move files to trash:', error);
      if (window.Toast) window.Toast.error('Failed to move files to trash');
    }
  }

  /**
   * Open a file (navigate to canvas)
   * @param {string} fileId - File ID
   */
  function openFile(fileId) {
    const state = window.WorkspaceHubState.getState();
    if (state.selectedId) {
      window.location.href = `/workspaces/${encodeURIComponent(state.selectedId)}/canvas`;
    }
  }

  /**
   * Load files and directories for a workspace
   * @param {string} workspaceId - Workspace ID
   */
  async function loadFiles(workspaceId) {
    if (!workspaceId) return;

    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-loading">Loading files...</div>';
    }
    if (elements.directoriesList) {
      elements.directoriesList.innerHTML = '<div class="hub-loading">Loading directories...</div>';
    }

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      const workspace = await response.json();
      state.files = (workspace.attachments || []).filter(a => a.file_meta || a.type === 'image' || a.type === 'other');
      state.directories = workspace.directory_references || [];
      renderFiles(state.files);
      renderDirectories(state.directories);
    } catch (error) {
      console.error('Workspace hub failed to load files:', error);
      if (elements.filesList) {
        elements.filesList.innerHTML = '<div class="hub-empty">Unable to load files.</div>';
      }
      if (elements.directoriesList) {
        elements.directoriesList.innerHTML = '<div class="hub-empty">Unable to load directories.</div>';
      }
    }
  }

  /**
   * Render files list
   * @param {Array} files - Files array
   */
  function renderFiles(files) {
    const elements = window.WorkspaceHubState.getElements();
    const state = window.WorkspaceHubState.getState();

    if (!elements.filesList) return;

    if (!files || files.length === 0) {
      elements.filesList.innerHTML = '<div class="hub-empty">No files yet.</div>';
      return;
    }

    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('files');
    const selectedSet = state.selectedItems.files;

    const items = files.slice(0, 5).map((file) => {
      const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
      const size = file.file_meta ? formatFileSize(file.file_meta.size) : '';
      const icon = getFileIcon(file.type, file.file_meta?.mime);
      const isSelected = selectedSet.has(file.id);

      return `
        <div class="hub-file-item${isSelected ? ' selected' : ''}" data-file-id="${escapeHtml(file.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select file">
          </div>
          <div class="hub-file-icon">${icon}</div>
          <div class="hub-file-info">
            <div class="hub-file-title">${escapeHtml(title)}</div>
            ${size ? `<div class="hub-file-meta">${escapeHtml(size)}</div>` : ''}
          </div>
          <button class="hub-item-delete-btn" data-action="trash" title="Move to trash">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.filesList.innerHTML = items.join('');
    bindFileEvents();
  }

  /**
   * Render directories list
   * @param {Array} directories - Directories array
   */
  function renderDirectories(directories) {
    const elements = window.WorkspaceHubState.getElements();

    if (!elements.directoriesList) return;

    if (!directories || directories.length === 0) {
      elements.directoriesList.innerHTML = '<div class="hub-empty">No directories yet.</div>';
      return;
    }

    const items = directories.slice(0, 5).map((directory) => {
      const title = directory.name || 'Untitled Directory';
      const path = directory.path || 'Path unavailable';
      return `
        <div class="hub-directory-item" data-directory-id="${escapeHtml(directory.id)}">
          <div class="hub-directory-icon">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
            </svg>
          </div>
          <div class="hub-directory-info">
            <div class="hub-directory-title">${escapeHtml(title)}</div>
            <div class="hub-directory-path" title="${escapeHtml(path)}">${escapeHtml(path)}</div>
          </div>
          <button class="hub-item-delete-btn" data-action="delete-directory" title="Remove directory reference">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.directoriesList.innerHTML = items.join('');
    bindDirectoryEvents();
  }

  /**
   * Bind event handlers to file items
   */
  function bindFileEvents() {
    const elements = window.WorkspaceHubState.getElements();
    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('files');

    elements.filesList.querySelectorAll('.hub-file-item').forEach((item) => {
      const fileId = item.dataset.fileId;

      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          window.WorkspaceHubSelection.toggleItemSelection('files', fileId);
        });
      }

      item.querySelector('[data-action="trash"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        moveFileToTrash(fileId);
      });

      item.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          window.WorkspaceHubSelection.toggleItemSelection('files', fileId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openFile(fileId);
        }
      });
    });
  }

  /**
   * Delete a directory reference
   * @param {string} directoryId - Directory ID to delete
   */
  async function deleteDirectory(directoryId) {
    const state = window.WorkspaceHubState.getState();

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: 'Remove Directory Reference',
      message: 'Remove this directory reference? The actual folder on disk will not be deleted.',
      variant: 'delete'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/directories/${encodeURIComponent(directoryId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to remove directory reference');

      if (window.Toast) window.Toast.success('Directory reference removed');
      await loadFiles(state.selectedId);
    } catch (error) {
      console.error('Failed to remove directory reference:', error);
      if (window.Toast) window.Toast.error('Failed to remove directory reference');
    }
  }

  /**
   * Bind event handlers to directory items
   */
  function bindDirectoryEvents() {
    const elements = window.WorkspaceHubState.getElements();

    if (!elements.directoriesList) return;

    elements.directoriesList.querySelectorAll('.hub-directory-item').forEach((item) => {
      const directoryId = item.dataset.directoryId;

      item.querySelector('[data-action="delete-directory"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        deleteDirectory(directoryId);
      });
    });
  }

  // ============================================================================
  // File Upload Modal Functions
  // ============================================================================

  /**
   * Open add file modal
   */
  function openAddFileModal() {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!state.selectedId) return;

    // Reset modal state
    state.pendingFiles = [];
    if (elements.fileTitle) elements.fileTitle.value = '';
    if (elements.fileNotes) elements.fileNotes.value = '';
    if (elements.selectedFilesPreview) elements.selectedFilesPreview.style.display = 'none';
    if (elements.selectedFilesList) elements.selectedFilesList.innerHTML = '';
    if (elements.fileUploadProgress) elements.fileUploadProgress.style.display = 'none';
    if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = true;

    if (elements.addFileModal && window.bootstrap) {
      const modal = new bootstrap.Modal(elements.addFileModal);
      modal.show();
    }
  }

  /**
   * Update selected files preview in modal
   */
  function updateSelectedFilesPreview() {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!elements.selectedFilesPreview || !elements.selectedFilesList) return;

    if (state.pendingFiles.length === 0) {
      elements.selectedFilesPreview.style.display = 'none';
      if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = true;
      return;
    }

    elements.selectedFilesPreview.style.display = 'block';
    if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = false;

    const items = state.pendingFiles.map((file, index) => `
      <div class="hub-selected-file-item" data-index="${index}">
        <div class="hub-selected-file-info">
          <span class="hub-selected-file-name">${escapeHtml(file.name)}</span>
          <span class="hub-selected-file-size">${formatFileSize(file.size)}</span>
        </div>
        <button type="button" class="hub-selected-file-remove" data-index="${index}" title="Remove">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `);

    elements.selectedFilesList.innerHTML = items.join('');

    // Bind remove button handlers
    elements.selectedFilesList.querySelectorAll('.hub-selected-file-remove').forEach((btn) => {
      btn.addEventListener('click', (event) => {
        event.stopPropagation();
        const index = parseInt(btn.dataset.index, 10);
        state.pendingFiles.splice(index, 1);
        updateSelectedFilesPreview();
      });
    });
  }

  /**
   * Handle file drop zone drag over
   * @param {DragEvent} event - Drag event
   */
  function handleFileDropZoneDragOver(event) {
    event.preventDefault();
    event.stopPropagation();
    const elements = window.WorkspaceHubState.getElements();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.add('drag-active');
    }
  }

  /**
   * Handle file drop zone drag leave
   * @param {DragEvent} event - Drag event
   */
  function handleFileDropZoneDragLeave(event) {
    event.preventDefault();
    event.stopPropagation();
    const elements = window.WorkspaceHubState.getElements();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.remove('drag-active');
    }
  }

  /**
   * Handle file drop zone drop
   * @param {DragEvent} event - Drop event
   */
  function handleFileDropZoneDrop(event) {
    event.preventDefault();
    event.stopPropagation();
    const elements = window.WorkspaceHubState.getElements();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.remove('drag-active');
    }

    const files = event.dataTransfer?.files;
    if (files && files.length > 0) {
      addFilesToPending(Array.from(files));
    }
  }

  /**
   * Handle file input change
   * @param {Event} event - Change event
   */
  function handleFileInputChange(event) {
    const files = event.target?.files;
    if (files && files.length > 0) {
      addFilesToPending(Array.from(files));
      event.target.value = '';
    }
  }

  /**
   * Add files to pending upload list
   * @param {Array} files - Array of File objects
   */
  function addFilesToPending(files) {
    const state = window.WorkspaceHubState.getState();
    const maxSize = 10 * 1024 * 1024; // 10MB

    files.forEach((file) => {
      // Check if this is likely a folder
      const hasExtension = file.name.includes('.');
      const isLikelyFolder = (!file.type && file.size === 0) ||
                             (!file.type && !hasExtension && file.size < 4096);

      if (isLikelyFolder) {
        if (window.Toast) {
          window.Toast.info(
            'To add a folder, use the "Directory" button in the canvas toolbar instead.',
            { title: 'Folder Detected' }
          );
        }
        return;
      }

      if (file.size > maxSize) {
        if (window.Toast) {
          window.Toast.warning(`${file.name} exceeds 10MB limit`);
        }
        return;
      }

      // Avoid duplicates
      if (!state.pendingFiles.some((f) => f.name === file.name && f.size === file.size)) {
        state.pendingFiles.push(file);
      }
    });

    updateSelectedFilesPreview();
  }

  /**
   * Submit file upload
   */
  async function submitAddFile() {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!state.selectedId || state.pendingFiles.length === 0) return;

    const title = elements.fileTitle?.value?.trim() || '';
    const notes = elements.fileNotes?.value?.trim() || '';

    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.disabled = true;
      elements.addFileSubmitBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>Uploading...';
    }

    if (elements.fileUploadProgress) {
      elements.fileUploadProgress.style.display = 'block';
    }

    let successCount = 0;
    const total = state.pendingFiles.length;

    for (let i = 0; i < state.pendingFiles.length; i++) {
      const file = state.pendingFiles[i];
      const percent = Math.round(((i + 0.5) / total) * 100);

      if (elements.fileUploadPercent) {
        elements.fileUploadPercent.textContent = `${percent}%`;
      }
      if (elements.fileUploadProgressBar) {
        elements.fileUploadProgressBar.style.width = `${percent}%`;
      }

      try {
        await uploadFileAttachment(file, title, notes);
        successCount++;
      } catch (error) {
        console.error('Failed to upload file:', file.name, error);
        if (window.Toast) {
          window.Toast.error(`Failed to upload ${file.name}`);
        }
      }
    }

    // Complete progress
    if (elements.fileUploadPercent) {
      elements.fileUploadPercent.textContent = '100%';
    }
    if (elements.fileUploadProgressBar) {
      elements.fileUploadProgressBar.style.width = '100%';
    }

    // Close modal
    if (elements.addFileModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.addFileModal);
      if (modal) modal.hide();
    }

    state.pendingFiles = [];

    if (successCount > 0 && window.Toast) {
      window.Toast.success(`Uploaded ${successCount} file${successCount !== 1 ? 's' : ''}`);
    }

    await loadFiles(state.selectedId);

    if (window.EventBus) {
      EventBus.emit('workspace:files:updated', { workspaceId: state.selectedId });
    }

    // Reset button
    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.disabled = false;
      elements.addFileSubmitBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
          <path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/>
        </svg>
        Upload
      `;
    }
  }

  /**
   * Upload a single file attachment
   * @param {File} file - File to upload
   * @param {string} title - Optional title
   * @param {string} notes - Optional notes
   * @returns {Promise} Resolves with upload result
   */
  async function uploadFileAttachment(file, title, notes) {
    const state = window.WorkspaceHubState.getState();

    return new Promise((resolve, reject) => {
      const reader = new FileReader();

      reader.onload = async (e) => {
        try {
          let content = e.target.result;
          if (content.includes(',')) {
            content = content.split(',')[1];
          }

          let type = 'other';
          const mime = file.type || '';
          const ext = file.name.split('.').pop().toLowerCase();

          if (mime.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) {
            type = 'image';
          } else if (mime.includes('pdf') || ext === 'pdf') {
            type = 'pdf';
          } else if (mime.includes('text') || ['txt', 'md', 'json', 'xml', 'csv', 'html'].includes(ext)) {
            type = 'doc';
          }

          const attachment = {
            title: title || file.name,
            body: notes || '',
            type: type,
            file_meta: {
              name: file.name,
              size: file.size,
              mime: mime || 'application/octet-stream',
              content: content
            }
          };

          const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(attachment)
          });

          if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || 'Upload failed');
          }

          resolve(await response.json());
        } catch (error) {
          reject(error);
        }
      };

      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });
  }

  /**
   * Launch folder picker app
   * @param {Object} options - Options
   * @returns {Promise<boolean>} Success status
   */
  async function launchFolderPicker(options = {}) {
    const elements = window.WorkspaceHubState.getElements();

    try {
      const response = await fetch('/api/launch-folder-picker', { method: 'POST' });
      const result = await response.json();

      if (result.success) {
        if (options.closeModal && elements.addFileModal && window.bootstrap) {
          const modal = bootstrap.Modal.getInstance(elements.addFileModal);
          if (modal) modal.hide();
        }
        if (options.successMessage) {
          window.toastManager?.showToast(options.successMessage, 'info');
        }
        return true;
      }

      window.toastManager?.showToast(result.error || 'Failed to launch folder picker', 'error');
    } catch (error) {
      console.error('Failed to launch folder picker:', error);
      window.toastManager?.showToast('Failed to launch folder picker', 'error');
    }
    return false;
  }

  /**
   * Bind file upload modal events
   */
  function bindFileUploadEvents() {
    const elements = window.WorkspaceHubState.getElements();

    if (elements.fileDropZone) {
      elements.fileDropZone.addEventListener('click', () => elements.fileInput?.click());
      elements.fileDropZone.addEventListener('dragover', handleFileDropZoneDragOver);
      elements.fileDropZone.addEventListener('dragleave', handleFileDropZoneDragLeave);
      elements.fileDropZone.addEventListener('drop', handleFileDropZoneDrop);
    }
    if (elements.fileInput) {
      elements.fileInput.addEventListener('change', handleFileInputChange);
    }
    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.addEventListener('click', submitAddFile);
    }
    if (elements.addDirectoryPanelBtn) {
      elements.addDirectoryPanelBtn.addEventListener('click', async () => {
        const button = elements.addDirectoryPanelBtn;
        const original = button.innerHTML;
        button.disabled = true;
        button.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
        try {
          await launchFolderPicker({ successMessage: 'Folder picker opened. Select a folder to add it.' });
        } finally {
          button.disabled = false;
          button.innerHTML = original;
        }
      });
    }
  }

  // Expose files manager globally
  window.WorkspaceHubFiles = {
    moveFileToTrash,
    bulkMoveFilesToTrash,
    openFile,
    loadFiles,
    renderFiles,
    renderDirectories,
    deleteDirectory,
    openAddFileModal,
    updateSelectedFilesPreview,
    addFilesToPending,
    submitAddFile,
    uploadFileAttachment,
    launchFolderPicker,
    bindFileUploadEvents
  };
})();

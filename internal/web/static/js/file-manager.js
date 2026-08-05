/**
 * File Manager Module
 * Handles file uploads, drag-and-drop, and file operations for sessions
 */

class FileManager {
  constructor(sessionId) {
    this.sessionId = sessionId;
    this.baseUrl = '/api/sessions';
    this.files = [];
    this.uploads = new Map();
    this.eventSource = null;

    this.init();
  }

  init() {
    this.bindElements();
    this.bindEvents();
    this.loadFiles();
    this.connectSSE();
  }

  bindElements() {
    // Dropzone elements
    this.dropzone = document.getElementById('fileDropzone');
    this.fileInput = document.getElementById('fileInput');
    this.browseBtn = document.getElementById('browseFilesBtn');

    // Compact dropzone
    this.dropzoneCompact = document.getElementById('fileDropzoneCompact');
    this.fileInputCompact = document.getElementById('fileInputCompact');

    // File list elements
    this.fileListContainer = document.getElementById('fileListContainer');
    this.fileList = document.getElementById('fileList');
    this.fileListEmpty = document.getElementById('fileListEmpty');
    this.fileListLoading = document.getElementById('fileListLoading');
    this.fileCount = document.getElementById('fileCount');

    // Action buttons
    this.openFolderBtn = document.getElementById('openFolderBtn');
    this.refreshFilesBtn = document.getElementById('refreshFilesBtn');

    // Modals
    this.previewModal = document.getElementById('filePreviewModal');
    this.deleteModal = document.getElementById('fileDeleteModal');
    this.relinkModal = document.getElementById('fileRelinkModal');
  }

  bindEvents() {
    // Dropzone events
    if (this.dropzone) {
      this.dropzone.addEventListener('click', () => this.fileInput?.click());
      this.dropzone.addEventListener('dragover', e => this.handleDragOver(e));
      this.dropzone.addEventListener('dragleave', e => this.handleDragLeave(e));
      this.dropzone.addEventListener('drop', e => this.handleDrop(e));
    }

    if (this.browseBtn) {
      this.browseBtn.addEventListener('click', e => {
        e.stopPropagation();
        this.fileInput?.click();
      });
    }

    if (this.fileInput) {
      this.fileInput.addEventListener('change', e => this.handleFileSelect(e));
    }

    // Compact dropzone
    if (this.dropzoneCompact) {
      this.dropzoneCompact.addEventListener('click', () => this.fileInputCompact?.click());
    }

    if (this.fileInputCompact) {
      this.fileInputCompact.addEventListener('change', e => this.handleFileSelect(e));
    }

    // Action buttons
    if (this.openFolderBtn) {
      this.openFolderBtn.addEventListener('click', () => this.openFolder());
    }

    if (this.refreshFilesBtn) {
      this.refreshFilesBtn.addEventListener('click', () => this.loadFiles());
    }

    // File list actions (event delegation)
    if (this.fileList) {
      this.fileList.addEventListener('click', e => this.handleFileAction(e));
    }

    // Modal confirmations
    const confirmDeleteBtn = document.getElementById('confirmFileDelete');
    if (confirmDeleteBtn) {
      confirmDeleteBtn.addEventListener('click', () => this.confirmDelete());
    }

    const confirmRelinkBtn = document.getElementById('confirmRelink');
    if (confirmRelinkBtn) {
      confirmRelinkBtn.addEventListener('click', () => this.confirmRelink());
    }

    const downloadFromPreviewBtn = document.getElementById('downloadFromPreview');
    if (downloadFromPreviewBtn) {
      downloadFromPreviewBtn.addEventListener('click', () => this.downloadCurrentPreview());
    }

    // Global drag and drop on document
    document.addEventListener('dragover', e => e.preventDefault());
    document.addEventListener('drop', e => e.preventDefault());
  }

  // ===== Drag and Drop =====

  handleDragOver(e) {
    e.preventDefault();
    e.stopPropagation();
    this.dropzone?.classList.add('dragover');
  }

  handleDragLeave(e) {
    e.preventDefault();
    e.stopPropagation();
    if (!this.dropzone?.contains(e.relatedTarget)) {
      this.dropzone?.classList.remove('dragover');
    }
  }

  handleDrop(e) {
    e.preventDefault();
    e.stopPropagation();
    this.dropzone?.classList.remove('dragover');

    const files = e.dataTransfer?.files;
    if (files && files.length > 0) {
      this.processFiles(Array.from(files));
    }
  }

  handleFileSelect(e) {
    const files = e.target?.files;
    if (files && files.length > 0) {
      this.processFiles(Array.from(files));
      e.target.value = ''; // Reset input
    }
  }

  // ===== File Processing =====

  async processFiles(files) {
    // Validate file count
    const currentCount = this.files.length;
    const maxFiles = 50;

    if (currentCount + files.length > maxFiles) {
      this.showError(
        `Cannot add ${files.length} files. Maximum is ${maxFiles} files per session (current: ${currentCount}).`
      );
      return;
    }

    // Upload files directly (copy to session folder)
    await this.uploadFiles(files);
  }

  async uploadFiles(files) {
    for (const file of files) {
      const uploadId = this.generateId();
      this.showUploadProgress(uploadId, file.name);

      try {
        await this.uploadFile(file, uploadId);
        this.removeUploadProgress(uploadId);
      } catch (error) {
        console.error('Upload failed:', error);
        this.updateUploadStatus(uploadId, 'Failed: ' + error.message);
      }
    }

    await this.loadFiles();
  }

  async uploadFile(file, uploadId) {
    const formData = new FormData();
    formData.append('file', file);

    const xhr = new XMLHttpRequest();

    return new Promise((resolve, reject) => {
      xhr.upload.addEventListener('progress', e => {
        if (e.lengthComputable) {
          const percent = Math.round((e.loaded / e.total) * 100);
          this.updateUploadProgress(uploadId, percent);
        }
      });

      xhr.addEventListener('load', () => {
        if (xhr.status === 201) {
          resolve(JSON.parse(xhr.responseText));
        } else {
          // Try to get error message from response body
          let errorMsg = xhr.statusText || 'Upload failed';
          try {
            const resp = JSON.parse(xhr.responseText);
            if (resp.message) errorMsg = resp.message;
          } catch (e) {
            // Ignore JSON parse errors
          }
          reject(new Error(errorMsg));
        }
      });

      xhr.addEventListener('error', () => reject(new Error('Network error')));
      xhr.addEventListener('abort', () => reject(new Error('Upload cancelled')));

      xhr.open('POST', `${this.baseUrl}/${this.sessionId}/files/upload`);
      xhr.send(formData);

      this.uploads.set(uploadId, xhr);
    });
  }

  // ===== File List =====

  async loadFiles() {
    if (this.fileListLoading) this.fileListLoading.style.display = 'flex';
    if (this.fileListEmpty) this.fileListEmpty.style.display = 'none';

    try {
      const response = await fetch(`${this.baseUrl}/${this.sessionId}/files`);
      if (!response.ok) throw new Error('Failed to load files');

      const data = await response.json();
      this.files = data.files || [];
      this.renderFileList();
    } catch (error) {
      console.error('Failed to load files:', error);
      this.showError('Failed to load files');
    } finally {
      if (this.fileListLoading) this.fileListLoading.style.display = 'none';
    }
  }

  renderFileList() {
    if (!this.fileList) return;

    // Update count (both in file list header and sidebar)
    if (this.fileCount) {
      this.fileCount.textContent = this.files.length;
    }
    const sessionFileCount = document.getElementById('sessionFileCount');
    if (sessionFileCount) {
      sessionFileCount.textContent = this.files.length;
    }

    // Show empty state if no files
    if (this.files.length === 0) {
      if (this.fileListEmpty) this.fileListEmpty.style.display = 'flex';
      // Remove all file items but keep empty state
      const items = this.fileList.querySelectorAll('.file-item');
      items.forEach(item => item.remove());
      return;
    }

    if (this.fileListEmpty) this.fileListEmpty.style.display = 'none';

    // Render files
    const html = this.files.map(file => this.renderFileItem(file)).join('');

    // Keep upload progress items
    const uploadItems = this.fileList.querySelectorAll('.file-upload-progress');
    this.fileList.innerHTML = html;
    uploadItems.forEach(item => this.fileList.prepend(item));
  }

  renderFileItem(file) {
    const iconClass = file.is_link ? 'file-icon-link' : 'file-icon-default';
    const statusBadge =
      file.status === 'broken'
        ? `
      <span class="file-status-broken" title="Link is broken - original file not found">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
      </span>
    `
        : '';

    const actions =
      file.status === 'broken'
        ? `
      <button class="file-action-btn file-relink-btn" title="Re-link file" data-action="relink">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
        </svg>
      </button>
    `
        : `
      <button class="file-action-btn file-preview-btn" title="Preview" data-action="preview">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12,9A3,3 0 0,0 9,12A3,3 0 0,0 12,15A3,3 0 0,0 15,12A3,3 0 0,0 12,9M12,17A5,5 0 0,1 7,12A5,5 0 0,1 12,7A5,5 0 0,1 17,12A5,5 0 0,1 12,17M12,4.5C7,4.5 2.73,7.61 1,12C2.73,16.39 7,19.5 12,19.5C17,19.5 21.27,16.39 23,12C21.27,7.61 17,4.5 12,4.5Z"/>
        </svg>
      </button>
      <button class="file-action-btn file-download-btn" title="Download" data-action="download">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
        </svg>
      </button>
    `;

    return `
      <div class="file-item" data-file-id="${file.id}" data-file-name="${this.escapeHtml(file.name)}">
        <div class="file-item-icon">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="${iconClass}">
            ${
              file.is_link
                ? '<path d="M10.59,13.41C11,13.8 11,14.44 10.59,14.83C10.2,15.22 9.56,15.22 9.17,14.83C7.22,12.88 7.22,9.71 9.17,7.76V7.76L12.71,4.22C14.66,2.27 17.83,2.27 19.78,4.22C21.73,6.17 21.73,9.34 19.78,11.29L18.29,12.78C18.3,11.96 18.17,11.14 17.89,10.36L18.36,9.88C19.54,8.71 19.54,6.81 18.36,5.64C17.19,4.46 15.29,4.46 14.12,5.64L10.59,9.17C9.41,10.34 9.41,12.24 10.59,13.41M13.41,9.17C13.8,8.78 14.44,8.78 14.83,9.17C16.78,11.12 16.78,14.29 14.83,16.24V16.24L11.29,19.78C9.34,21.73 6.17,21.73 4.22,19.78C2.27,17.83 2.27,14.66 4.22,12.71L5.71,11.22C5.7,12.04 5.83,12.86 6.11,13.65L5.64,14.12C4.46,15.29 4.46,17.19 5.64,18.36C6.81,19.54 8.71,19.54 9.88,18.36L13.41,14.83C14.59,13.66 14.59,11.76 13.41,10.59C13,10.2 13,9.56 13.41,9.17Z"/>'
                : '<path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/>'
            }
          </svg>
          ${statusBadge}
        </div>
        <div class="file-item-info">
          <div class="file-item-name" title="${this.escapeHtml(file.name)}">${this.escapeHtml(file.name)}</div>
          <div class="file-item-meta">
            <span class="file-item-size">${this.formatFileSize(file.size)}</span>
            <span class="file-item-type">${file.mime_type}</span>
          </div>
        </div>
        <div class="file-item-actions">
          ${actions}
          <button class="file-action-btn file-delete-btn" title="Remove from session" data-action="delete">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      </div>
    `;
  }

  // ===== File Actions =====

  handleFileAction(e) {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;

    const action = btn.dataset.action;
    const fileItem = btn.closest('.file-item');
    const fileId = fileItem?.dataset.fileId;
    const fileName = fileItem?.dataset.fileName;

    switch (action) {
      case 'preview':
        this.previewFile(fileId, fileName);
        break;
      case 'download':
        this.downloadFile(fileId, fileName);
        break;
      case 'delete':
        this.showDeleteDialog(fileId, fileName);
        break;
      case 'relink':
        this.showRelinkDialog(fileId, fileName);
        break;
      case 'cancel':
        this.cancelUpload(btn.closest('.file-upload-progress')?.dataset.uploadId);
        break;
    }
  }

  async previewFile(fileId, fileName) {
    this.currentPreviewFileId = fileId;
    this.currentPreviewFileName = fileName;

    const modal = bootstrap.Modal.getOrCreateInstance(this.previewModal);
    modal.show();

    const content = document.getElementById('filePreviewContent');
    const nameEl = document.getElementById('previewFileName');
    const infoEl = document.getElementById('previewFileInfo');

    if (nameEl) nameEl.textContent = fileName;
    if (content)
      content.innerHTML =
        '<div class="file-preview-loading"><div class="spinner-border" role="status"></div></div>';

    try {
      const response = await fetch(`${this.baseUrl}/${this.sessionId}/files/${fileId}/download`);
      if (!response.ok) throw new Error('Failed to load file');

      const contentType = response.headers.get('content-type') || 'application/octet-stream';
      const blob = await response.blob();

      if (infoEl) {
        infoEl.textContent = `${this.formatFileSize(blob.size)} - ${contentType}`;
      }

      if (contentType.startsWith('image/')) {
        const url = URL.createObjectURL(blob);
        content.innerHTML = `<img src="${url}" class="file-preview-image" alt="${this.escapeHtml(fileName)}">`;
      } else if (
        contentType.startsWith('text/') ||
        contentType.includes('json') ||
        contentType.includes('xml')
      ) {
        const text = await blob.text();
        content.innerHTML = `<pre class="file-preview-text">${this.escapeHtml(text)}</pre>`;
      } else {
        content.innerHTML = `
          <div class="file-preview-unsupported">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor">
              <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/>
            </svg>
            <p>Preview not available for this file type</p>
            <p class="small text-muted">${contentType}</p>
          </div>
        `;
      }
    } catch (error) {
      console.error('Preview failed:', error);
      content.innerHTML = `<div class="file-preview-unsupported"><p>Failed to load preview</p></div>`;
    }
  }

  downloadFile(fileId, fileName) {
    const link = document.createElement('a');
    link.href = `${this.baseUrl}/${this.sessionId}/files/${fileId}/download`;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  }

  downloadCurrentPreview() {
    if (this.currentPreviewFileId && this.currentPreviewFileName) {
      this.downloadFile(this.currentPreviewFileId, this.currentPreviewFileName);
    }
  }

  showDeleteDialog(fileId, fileName) {
    this.deleteFileId = fileId;

    const nameEl = document.getElementById('deleteFileName');
    const noteEl = document.getElementById('deleteFileNote');

    if (nameEl) nameEl.textContent = fileName;

    const file = this.files.find(f => f.id === fileId);
    if (noteEl) {
      noteEl.textContent = file?.is_link
        ? 'The original file will not be affected.'
        : 'This will permanently delete the file from the session folder.';
    }

    const modal = bootstrap.Modal.getOrCreateInstance(this.deleteModal);
    modal.show();
  }

  async confirmDelete() {
    if (!this.deleteFileId) return;

    const modal = bootstrap.Modal.getInstance(this.deleteModal);

    try {
      const response = await fetch(`${this.baseUrl}/${this.sessionId}/files/${this.deleteFileId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete file');

      modal?.hide();
      await this.loadFiles();
    } catch (error) {
      console.error('Delete failed:', error);
      this.showError('Failed to delete file');
    }

    this.deleteFileId = null;
  }

  showRelinkDialog(fileId, fileName) {
    this.relinkFileId = fileId;

    const nameEl = document.getElementById('relinkFileName');
    const pathEl = document.getElementById('relinkOriginalPath');
    const newPathEl = document.getElementById('relinkNewPath');

    const file = this.files.find(f => f.id === fileId);

    if (nameEl) nameEl.textContent = fileName;
    if (pathEl) pathEl.textContent = file?.original_path || 'Unknown';
    if (newPathEl) newPathEl.value = '';

    const modal = bootstrap.Modal.getOrCreateInstance(this.relinkModal);
    modal.show();
  }

  async confirmRelink() {
    const newPath = document.getElementById('relinkNewPath')?.value;
    if (!this.relinkFileId || !newPath) return;

    const modal = bootstrap.Modal.getInstance(this.relinkModal);

    try {
      const response = await fetch(
        `${this.baseUrl}/${this.sessionId}/files/${this.relinkFileId}/relink`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: newPath })
        }
      );

      if (!response.ok) throw new Error('Failed to relink file');

      modal?.hide();
      await this.loadFiles();
    } catch (error) {
      console.error('Relink failed:', error);
      this.showError('Failed to relink file');
    }

    this.relinkFileId = null;
  }

  async openFolder() {
    try {
      const response = await fetch(`${this.baseUrl}/${this.sessionId}/folder/open`, {
        method: 'POST'
      });

      if (!response.ok) throw new Error('Failed to open folder');
    } catch (error) {
      console.error('Open folder failed:', error);
      this.showError('Failed to open folder');
    }
  }

  // ===== Upload Progress =====

  showUploadProgress(uploadId, fileName) {
    const html = `
      <div class="file-upload-progress" data-upload-id="${uploadId}">
        <div class="file-item-icon">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="file-icon-uploading">
            <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/>
          </svg>
        </div>
        <div class="file-item-info">
          <div class="file-item-name">${this.escapeHtml(fileName)}</div>
          <div class="file-upload-bar">
            <div class="file-upload-progress-bar" style="width: 0%"></div>
          </div>
          <div class="file-upload-status">Uploading...</div>
        </div>
        <button class="file-action-btn file-cancel-btn" title="Cancel upload" data-action="cancel">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `;

    if (this.fileListEmpty) this.fileListEmpty.style.display = 'none';
    this.fileList?.insertAdjacentHTML('afterbegin', html);
  }

  updateUploadProgress(uploadId, percent) {
    const item = this.fileList?.querySelector(`[data-upload-id="${uploadId}"]`);
    if (!item) return;

    const bar = item.querySelector('.file-upload-progress-bar');
    const status = item.querySelector('.file-upload-status');

    if (bar) bar.style.width = `${percent}%`;
    if (status) status.textContent = `${percent}% uploaded`;
  }

  updateUploadStatus(uploadId, status) {
    const item = this.fileList?.querySelector(`[data-upload-id="${uploadId}"]`);
    if (!item) return;

    const statusEl = item.querySelector('.file-upload-status');
    if (statusEl) statusEl.textContent = status;
  }

  removeUploadProgress(uploadId) {
    const item = this.fileList?.querySelector(`[data-upload-id="${uploadId}"]`);
    item?.remove();
    this.uploads.delete(uploadId);
  }

  cancelUpload(uploadId) {
    const xhr = this.uploads.get(uploadId);
    if (xhr) {
      xhr.abort();
      this.removeUploadProgress(uploadId);
    }
  }

  // ===== SSE Connection =====

  connectSSE() {
    if (this.eventSource) {
      this.eventSource.close();
    }

    this.eventSource = new EventSource(`${this.baseUrl}/${this.sessionId}/files/events`);

    this.eventSource.onmessage = event => {
      try {
        const data = JSON.parse(event.data);
        this.handleSSEEvent(data);
      } catch (e) {
        console.error('Failed to parse SSE event:', e);
      }
    };

    this.eventSource.onerror = () => {
      console.warn('SSE connection error, reconnecting...');
      setTimeout(() => this.connectSSE(), 5000);
    };
  }

  handleSSEEvent(event) {
    switch (event.type) {
      case 'create':
      case 'modify':
      case 'remove':
        this.loadFiles();
        break;
    }
  }

  disconnect() {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
  }

  // ===== Utilities =====

  generateId() {
    return 'upload_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
  }

  formatFileSize(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  showError(message) {
    // Use existing toast system if available, otherwise alert
    if (window.showToast) {
      window.showToast(message, 'error');
    } else {
      console.error(message);
    }
  }
}

// Export for use
window.FileManager = FileManager;

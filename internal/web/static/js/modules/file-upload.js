// File Upload Module
// Handles file uploads and attachment management for chat

let uploadedFiles = [];
let attachedNotes = [];
let availableChatNotes = [];
let availableChatNotesWorkspaceId = '';
let chatNotesFilter = '';

// Initialize file upload functionality
function initFileUpload() {
  const fileInput = document.getElementById('fileUpload');
  const clearFilesBtn = document.getElementById('clearFilesBtn');
  const directoryBtn = document.getElementById('attachDirectoryBtn');
  const attachNotesBtn = document.getElementById('attachNotesBtn');
  const clearAttachedNotesBtn = document.getElementById('clearAttachedNotesBtn');
  const notesSearchInput = document.getElementById('chatNotesSearchInput');
  const notesRefreshBtn = document.getElementById('chatNotesPickerRefreshBtn');
  const notesModal = document.getElementById('chatNotesModal');

  if (fileInput) {
    fileInput.addEventListener('change', handleFileSelect);
  }

  if (clearFilesBtn) {
    clearFilesBtn.addEventListener('click', clearAllFiles);
  }

  if (directoryBtn) {
    directoryBtn.addEventListener('click', openFolderPickerForChat);
  }

  if (attachNotesBtn) {
    attachNotesBtn.addEventListener('click', () => {
      void openChatNotesModal();
    });
  }

  if (clearAttachedNotesBtn) {
    clearAttachedNotesBtn.addEventListener('click', clearAllAttachedNotes);
  }

  if (notesSearchInput) {
    notesSearchInput.addEventListener('input', event => {
      chatNotesFilter = String(event?.target?.value || '')
        .trim()
        .toLowerCase();
      renderChatNotesPickerList();
    });
  }

  if (notesRefreshBtn) {
    notesRefreshBtn.addEventListener('click', () => {
      void loadWorkspaceNotesForChat({ force: true });
    });
  }

  if (notesModal && !notesModal.dataset.notesPickerBound) {
    notesModal.addEventListener('hidden.bs.modal', () => {
      const searchInput = document.getElementById('chatNotesSearchInput');
      chatNotesFilter = '';
      if (searchInput) {
        searchInput.value = '';
      }
    });
    notesModal.dataset.notesPickerBound = '1';
  }

  // Initialize drag and drop
  initDragAndDrop();
  updateAttachNotesButton();
  updateAttachedNotesList();
}

// Open folder picker to add a directory reference for chat
async function openFolderPickerForChat() {
  const activeSession = window.sessionManager?.getActiveSession?.();
  const workspaceId = activeSession?.folder_id;

  if (!workspaceId) {
    if (window.Toast) {
      Toast.warning('Please select a chat session in a workspace first');
    }
    return;
  }

  const btn = document.getElementById('attachDirectoryBtn');
  const originalHtml = btn?.innerHTML;
  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
  }

  try {
    const response = await fetch('/api/launch-folder-picker', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workspace_id: workspaceId })
    });
    const result = await response.json();

    if (result.success) {
      if (window.Toast) {
        Toast.info('Folder picker opened. Select a folder to add it to the workspace.');
      }
    } else {
      if (window.Toast) {
        Toast.error(result.error || 'Failed to open folder picker');
      }
    }
  } catch (error) {
    console.error('Failed to open folder picker:', error);
    if (window.Toast) {
      Toast.error('Failed to open folder picker');
    }
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerHTML = originalHtml;
    }
  }
}

const escapeFileUploadHtml =
  typeof window.escapeHtml === 'function'
    ? window.escapeHtml.bind(window)
    : function escapeFileUploadHtmlFallback(value) {
        return String(value || '')
          .replace(/&/g, '&amp;')
          .replace(/</g, '&lt;')
          .replace(/>/g, '&gt;')
          .replace(/"/g, '&quot;')
          .replace(/'/g, '&#39;');
      };

function getActiveWorkspaceIdForChat() {
  return String(window.sessionManager?.getActiveSession?.()?.folder_id || '').trim();
}

function truncateNoteText(value, maxLength = 120) {
  const text = String(value || '').trim();
  if (!text) return '';
  if (text.length <= maxLength) return text;
  return `${text.slice(0, Math.max(0, maxLength - 1)).trim()}…`;
}

function setChatNotesPickerStatus(message, tone = 'neutral') {
  const status = document.getElementById('chatNotesPickerStatus');
  if (!status) return;

  const text = String(message || '').trim();
  if (!text) {
    status.style.display = 'none';
    status.textContent = '';
    return;
  }

  const palette = {
    neutral: {
      background: 'var(--bg-secondary)',
      border: '1px solid var(--border-color)',
      color: 'var(--text-secondary)'
    },
    warning: {
      background: 'rgba(245, 158, 11, 0.08)',
      border: '1px solid rgba(245, 158, 11, 0.3)',
      color: '#fbbf24'
    },
    success: {
      background: 'rgba(34, 197, 94, 0.08)',
      border: '1px solid rgba(34, 197, 94, 0.28)',
      color: '#86efac'
    }
  };
  const style = palette[tone] || palette.neutral;

  status.style.display = 'block';
  status.style.background = style.background;
  status.style.border = style.border;
  status.style.color = style.color;
  status.textContent = text;
}

function updateAttachNotesButton() {
  const countBadge = document.getElementById('attachNotesBtnCount');
  const button = document.getElementById('attachNotesBtn');
  const count = attachedNotes.length;

  if (countBadge) {
    countBadge.textContent = String(count);
    countBadge.style.display = count > 0 ? 'inline-flex' : 'none';
  }

  if (button) {
    button.title =
      count > 0
        ? `Attach workspace notes to message (${count} selected)`
        : 'Attach workspace notes to message';
  }
}

function updateAttachedNotesList() {
  const notesArea = document.getElementById('attachedNotesArea');
  const notesList = document.getElementById('attachedNotesList');

  if (!notesArea || !notesList) return;

  if (attachedNotes.length === 0) {
    notesArea.style.display = 'none';
    notesList.innerHTML = '';
    updateAttachNotesButton();
    return;
  }

  notesArea.style.display = 'block';
  notesList.innerHTML = attachedNotes
    .map(
      note => `
    <div class="chat-note-chip" style="display: inline-flex; align-items: center; gap: 6px; padding: 4px 8px; background: rgba(255, 122, 68, 0.08); border: 1px solid rgba(255, 122, 68, 0.22); border-radius: 999px; font-size: 12px; color: var(--text-primary);">
      <span aria-hidden="true">📝</span>
      <span>${escapeFileUploadHtml(truncateNoteText(note.name, 30))}</span>
      <button type="button" class="btn-remove-note-chip" data-note-id="${escapeFileUploadHtml(note.id)}" aria-label="Remove note ${escapeFileUploadHtml(note.name)}" style="background: none; border: none; color: var(--text-muted); cursor: pointer; padding: 0; margin-left: 2px; font-size: 14px; line-height: 1;">×</button>
    </div>
  `
    )
    .join('');

  notesList.querySelectorAll('.btn-remove-note-chip').forEach(button => {
    button.addEventListener('click', event => {
      const noteId = String(event.currentTarget?.dataset?.noteId || '').trim();
      if (noteId) {
        removeAttachedNote(noteId);
      }
    });
  });

  updateAttachNotesButton();
}

function removeAttachedNote(noteId) {
  attachedNotes = attachedNotes.filter(note => String(note.id) !== String(noteId));
  updateAttachedNotesList();
  renderChatNotesPickerList();
}

function clearAllAttachedNotes() {
  attachedNotes = [];
  updateAttachedNotesList();
  renderChatNotesPickerList();
}

function getAttachedChatNotes() {
  const workspaceId = getActiveWorkspaceIdForChat();
  if (!workspaceId) {
    if (attachedNotes.length > 0) {
      attachedNotes = [];
      updateAttachedNotesList();
      renderChatNotesPickerList();
    }
    return [];
  }

  const scopedNotes = attachedNotes.filter(
    note => !note.workspace_id || note.workspace_id === workspaceId
  );
  if (scopedNotes.length !== attachedNotes.length) {
    attachedNotes = scopedNotes;
    updateAttachedNotesList();
    renderChatNotesPickerList();
  }

  return attachedNotes.slice();
}

function clearAttachedNotesAfterSend() {
  attachedNotes = [];
  updateAttachedNotesList();
  renderChatNotesPickerList();
}

function isNoteAttached(noteId) {
  return attachedNotes.some(note => String(note.id) === String(noteId));
}

function toggleAttachedNote(note) {
  if (!note || !note.id) {
    return;
  }

  if (isNoteAttached(note.id)) {
    removeAttachedNote(note.id);
    return;
  }

  attachedNotes.push({
    id: note.id,
    name: note.name || 'Untitled Note',
    preview: note.preview || '',
    workspace_id:
      note.workspace_id || availableChatNotesWorkspaceId || getActiveWorkspaceIdForChat()
  });
  updateAttachedNotesList();
  renderChatNotesPickerList();
}

function renderChatNotesPickerList() {
  const list = document.getElementById('chatNotesPickerList');
  if (!list) {
    return;
  }

  const filteredNotes = availableChatNotes.filter(note => {
    if (!chatNotesFilter) return true;
    const haystack = `${note?.name || ''} ${note?.preview || ''}`.toLowerCase();
    return haystack.includes(chatNotesFilter);
  });

  if (availableChatNotes.length === 0) {
    list.innerHTML = '';
    return;
  }

  if (filteredNotes.length === 0) {
    setChatNotesPickerStatus('No notes match your search.', 'warning');
    list.innerHTML = '';
    return;
  }

  setChatNotesPickerStatus(
    attachedNotes.length > 0
      ? `${attachedNotes.length} note${attachedNotes.length === 1 ? '' : 's'} selected for this message.`
      : 'Select notes to attach as chat context.',
    attachedNotes.length > 0 ? 'success' : 'neutral'
  );

  list.innerHTML = filteredNotes
    .map(note => {
      const selected = isNoteAttached(note.id);
      const preview = truncateNoteText(note.preview || '', 140) || 'No preview available yet.';
      return `
      <button
        type="button"
        class="chat-note-picker-item"
        data-note-id="${escapeFileUploadHtml(note.id)}"
        style="display: flex; width: 100%; gap: 12px; align-items: flex-start; text-align: left; padding: 12px 14px; border-radius: 12px; border: 1px solid ${selected ? 'rgba(255, 122, 68, 0.35)' : 'var(--border-color)'}; background: ${selected ? 'rgba(255, 122, 68, 0.08)' : 'var(--bg-secondary)'}; color: var(--text-primary); transition: all 0.18s ease;">
        <div style="display: flex; align-items: center; justify-content: center; width: 24px; height: 24px; border-radius: 999px; background: ${selected ? 'rgba(255, 122, 68, 0.2)' : 'rgba(148, 163, 184, 0.12)'}; color: ${selected ? '#ff9a6a' : 'var(--text-secondary)'}; flex-shrink: 0;">
          ${selected ? '✓' : '📝'}
        </div>
        <div style="min-width: 0; flex: 1;">
          <div style="font-size: 13px; font-weight: 600; color: var(--text-primary); margin-bottom: 4px;">${escapeFileUploadHtml(note.name || 'Untitled Note')}</div>
          <div style="font-size: 12px; line-height: 1.45; color: var(--text-secondary);">${escapeFileUploadHtml(preview)}</div>
        </div>
        <div style="flex-shrink: 0; font-size: 11px; font-weight: 700; letter-spacing: 0.04em; text-transform: uppercase; color: ${selected ? '#ff9a6a' : 'var(--text-secondary)'};">
          ${selected ? 'Attached' : 'Attach'}
        </div>
      </button>
    `;
    })
    .join('');

  list.querySelectorAll('.chat-note-picker-item').forEach(button => {
    button.addEventListener('click', event => {
      const noteId = String(event.currentTarget?.dataset?.noteId || '').trim();
      const note = availableChatNotes.find(item => String(item.id) === noteId);
      toggleAttachedNote(note);
    });
  });
}

async function loadWorkspaceNotesForChat(options = {}) {
  const workspaceId = String(options.workspaceId || getActiveWorkspaceIdForChat() || '').trim();
  const force = Boolean(options.force);
  const refreshButton = document.getElementById('chatNotesPickerRefreshBtn');
  const list = document.getElementById('chatNotesPickerList');

  if (!workspaceId) {
    availableChatNotes = [];
    availableChatNotesWorkspaceId = '';
    if (list) list.innerHTML = '';
    setChatNotesPickerStatus('Select a workspace chat session first to attach notes.', 'warning');
    return;
  }

  const scopedNotes = attachedNotes.filter(
    note => !note.workspace_id || note.workspace_id === workspaceId
  );
  if (scopedNotes.length !== attachedNotes.length) {
    attachedNotes = scopedNotes;
    updateAttachedNotesList();
  }

  if (!force && availableChatNotesWorkspaceId === workspaceId && availableChatNotes.length > 0) {
    renderChatNotesPickerList();
    return;
  }

  if (refreshButton) {
    refreshButton.disabled = true;
    refreshButton.dataset.originalText = refreshButton.textContent || 'Refresh';
    refreshButton.textContent = 'Loading...';
  }
  setChatNotesPickerStatus('Loading workspace notes...', 'neutral');
  if (list) {
    list.innerHTML = '';
  }

  try {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
    if (!response.ok) {
      throw new Error('Failed to load workspace notes');
    }
    const data = await response.json().catch(() => ({}));
    availableChatNotes = Array.isArray(data?.notes) ? data.notes : [];
    availableChatNotesWorkspaceId = workspaceId;

    if (availableChatNotes.length === 0) {
      setChatNotesPickerStatus('No workspace notes available yet.', 'warning');
      if (list) list.innerHTML = '';
      return;
    }

    renderChatNotesPickerList();
  } catch (error) {
    console.error('Failed to load chat note attachments:', error);
    availableChatNotes = [];
    availableChatNotesWorkspaceId = workspaceId;
    if (list) list.innerHTML = '';
    setChatNotesPickerStatus('Failed to load workspace notes.', 'warning');
  } finally {
    if (refreshButton) {
      refreshButton.disabled = false;
      refreshButton.textContent = refreshButton.dataset.originalText || 'Refresh';
    }
  }
}

async function openChatNotesModal() {
  const workspaceId = getActiveWorkspaceIdForChat();
  if (!workspaceId) {
    if (window.Toast) {
      Toast.warning('Please select a workspace chat session first');
    }
    return;
  }

  const modalElement = document.getElementById('chatNotesModal');
  if (!modalElement || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
    if (window.Toast) {
      Toast.error('Notes picker is unavailable');
    }
    return;
  }

  const modal = bootstrap.Modal.getOrCreateInstance(modalElement);
  modal.show();
  await loadWorkspaceNotesForChat({
    workspaceId,
    force: availableChatNotesWorkspaceId !== workspaceId
  });
}

// Initialize drag and drop functionality
function initDragAndDrop() {
  const chatContainer = document.getElementById('chatContainer');
  const inputWrapper = document.getElementById('inputWrapper');
  const inputContainer = document.getElementById('inputContainer');

  if (!inputWrapper) {
    return;
  }

  let dragCounter = 0;

  function showDropZone() {
    inputWrapper.classList.add('drag-active');
  }

  function hideDropZone() {
    inputWrapper.classList.remove('drag-active');
    dragCounter = 0;
  }

  // Prevent default on document to stop browser from opening files
  document.addEventListener('dragover', e => e.preventDefault());
  document.addEventListener('drop', e => e.preventDefault());

  // Listen on chat container for drag events
  if (chatContainer) {
    chatContainer.addEventListener('dragenter', e => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        dragCounter++;
        showDropZone();
      }
    });

    chatContainer.addEventListener('dragover', e => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        e.dataTransfer.dropEffect = 'copy';
      }
    });

    chatContainer.addEventListener('dragleave', e => {
      e.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) {
        hideDropZone();
      }
    });

    chatContainer.addEventListener('drop', async e => {
      e.preventDefault();
      hideDropZone();
      const files = e.dataTransfer.files;
      if (files && files.length > 0) {
        await processFiles(Array.from(files));
      }
    });
  }

  // Listen on input container (the whole input card area)
  if (inputContainer) {
    inputContainer.addEventListener('dragenter', e => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        dragCounter++;
        showDropZone();
      }
    });

    inputContainer.addEventListener('dragover', e => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        e.dataTransfer.dropEffect = 'copy';
      }
    });

    inputContainer.addEventListener('dragleave', e => {
      e.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) {
        hideDropZone();
      }
    });

    inputContainer.addEventListener('drop', async e => {
      e.preventDefault();
      hideDropZone();
      const files = e.dataTransfer.files;
      if (files && files.length > 0) {
        await processFiles(Array.from(files));
      }
    });
  }
}

// Handle file selection from file input
async function handleFileSelect(event) {
  const files = Array.from(event.target.files);
  await processFiles(files);

  // Clear the input so the same file can be selected again
  event.target.value = '';
}

function isLikelyBase64(content) {
  if (!content || typeof content !== 'string') return false;
  if (content.length % 4 !== 0) return false;
  return /^[A-Za-z0-9+/=]+$/.test(content);
}

function inferContentEncoding(fileData) {
  if (fileData.encoding) return fileData.encoding;
  const type = (fileData.type || '').toLowerCase();
  if (
    type.startsWith('text/') ||
    type.includes('json') ||
    type.includes('xml') ||
    type.includes('csv') ||
    type.includes('markdown') ||
    type.includes('html')
  ) {
    return 'text';
  }
  return isLikelyBase64(fileData.content) ? 'base64' : 'text';
}

// Process files (shared between file input and drag-drop)
async function processFiles(files) {
  // Allowed file extensions
  const allowedExtensions = [
    'txt',
    'md',
    'pdf',
    'doc',
    'docx',
    'csv',
    'json',
    'xml',
    'html',
    'mp3',
    'wav',
    'flac',
    'ogg',
    'zip',
    'pptx',
    'xlsx',
    'png',
    'jpg',
    'jpeg',
    'gif',
    'webp'
  ];

  let successCount = 0;

  for (const file of files) {
    // Check file extension
    const ext = file.name.split('.').pop().toLowerCase();
    if (!allowedExtensions.includes(ext)) {
      if (window.Toast) {
        Toast.warning(`File type .${ext} is not supported`, { title: 'Unsupported File' });
      }
      continue;
    }

    // Check file size (max 10MB)
    if (file.size > 10 * 1024 * 1024) {
      if (window.Toast) {
        Toast.warning(`${file.name} exceeds 10MB limit`, { title: 'File Too Large' });
      }
      continue;
    }

    try {
      const result = await readFileContent(file);

      // Determine MIME type - use actual type or infer from extension
      let mimeType = file.type;
      if (!mimeType) {
        const mimeMap = {
          pdf: 'application/pdf',
          wav: 'audio/wav',
          mp3: 'audio/mpeg',
          aiff: 'audio/aiff',
          aif: 'audio/aiff',
          flac: 'audio/flac',
          ogg: 'audio/ogg',
          mid: 'audio/midi',
          midi: 'audio/midi',
          zip: 'application/zip',
          pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
          xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
          png: 'image/png',
          jpg: 'image/jpeg',
          jpeg: 'image/jpeg',
          gif: 'image/gif',
          webp: 'image/webp'
        };
        mimeType = mimeMap[ext] || 'application/octet-stream';
      }

      uploadedFiles.push({
        name: file.name,
        type: mimeType,
        size: file.size,
        content: result.binaryContent || result.content, // Prefer binary content for files that have it
        encoding: result.binaryContent ? 'base64' : 'text'
      });

      successCount++;
    } catch (error) {
      console.error(`Error reading file ${file.name}:`, error);
      if (window.Toast) {
        Toast.error(`Failed to read ${file.name}`, { title: 'Upload Error' });
      }
    }
  }

  updateFilesList();

  // Show success toast if files were added
  if (successCount > 0) {
    if (window.Toast) {
      const message = successCount === 1 ? '1 file attached' : `${successCount} files attached`;
      Toast.success(message, { title: 'Files Ready' });
    }
  }
}

// Check if file is binary (PDF, DOCX, DOC, audio, images, etc.)
function isBinaryFile(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  return [
    'pdf',
    'docx',
    'doc',
    'pptx',
    'xlsx',
    'wav',
    'mp3',
    'aiff',
    'aif',
    'flac',
    'ogg',
    'mid',
    'midi',
    'zip',
    'png',
    'jpg',
    'jpeg',
    'gif',
    'webp'
  ].includes(ext);
}

// Check if file should be parsed for text (for LLM consumption)
function shouldParseForText(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  return ['pdf', 'docx', 'doc', 'pptx', 'xlsx'].includes(ext);
}

// Read file content - returns { content, binaryContent } for binary files
async function readFileContent(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();

    reader.onload = async e => {
      const content = e.target.result;

      // If it's a binary file, we need to handle it specially
      if (isBinaryFile(file.name)) {
        try {
          // content is already base64 from readAsDataURL
          // Remove the data URL prefix to get just the base64
          const base64 = content.split(',')[1];

          // For files that can be parsed for text (PDF, DOC), do that
          if (shouldParseForText(file.name)) {
            const response = await fetch('/api/files/parse', {
              method: 'POST',
              headers: {
                'Content-Type': 'application/json'
              },
              body: JSON.stringify({
                filename: file.name,
                content: base64
              })
            });

            const result = await response.json();

            if (result.error) {
              // If parsing fails, still include the file with just binary content
              console.warn(`Failed to parse ${file.name}: ${result.error}`);
              resolve({ content: `[Binary file: ${file.name}]`, binaryContent: base64 });
              return;
            }

            // Return both the parsed text (for LLM) and binary (for plugins)
            resolve({ content: result.text, binaryContent: base64 });
          } else {
            // For other binary files (audio, MIDI, ZIP), just store binary
            resolve({ content: `[Binary file: ${file.name}]`, binaryContent: base64 });
          }
        } catch (error) {
          // Even on error, try to preserve binary content
          console.error(`Error processing ${file.name}:`, error);
          const base64 = content.split(',')[1];
          resolve({ content: `[Binary file: ${file.name}]`, binaryContent: base64 });
        }
      } else {
        // For text files, just return the content
        resolve({ content: content, binaryContent: null });
      }
    };

    reader.onerror = e => {
      reject(e);
    };

    // Read binary files as data URL (base64), text files as text
    if (isBinaryFile(file.name)) {
      reader.readAsDataURL(file);
    } else {
      reader.readAsText(file);
    }
  });
}

// Update the files list display
function updateFilesList() {
  const filesArea = document.getElementById('uploadedFilesArea');
  const filesList = document.getElementById('uploadedFilesList');

  if (!filesArea || !filesList) return;

  if (uploadedFiles.length === 0) {
    filesArea.style.display = 'none';
    return;
  }

  filesArea.style.display = 'block';
  filesList.innerHTML = '';

  uploadedFiles.forEach((file, index) => {
    const fileChip = document.createElement('div');
    fileChip.className = 'file-chip';
    fileChip.style.cssText = `
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 4px 8px;
      background: var(--bg-secondary);
      border: 1px solid var(--border-color);
      border-radius: 4px;
      font-size: 12px;
      color: var(--text-primary);
    `;

    const fileIcon = getFileIcon(file.name);
    const fileName = file.name.length > 20 ? file.name.substring(0, 20) + '...' : file.name;
    const fileSize = formatFileSize(file.size);

    fileChip.innerHTML = `
      <span>${fileIcon}</span>
      <span>${fileName}</span>
      <span style="color: var(--text-muted); font-size: 10px;">(${fileSize})</span>
      <button type="button" class="btn-remove-file" data-index="${index}" aria-label="Remove file" style="background: none; border: none; color: var(--text-muted); cursor: pointer; padding: 0; margin-left: 4px; font-size: 14px;">×</button>
    `;

    filesList.appendChild(fileChip);
  });

  // Add click handlers for remove buttons
  document.querySelectorAll('.btn-remove-file').forEach(btn => {
    btn.addEventListener('click', e => {
      const index = parseInt(e.target.dataset.index);
      removeFile(index);
    });
  });
}

// Get file icon based on file extension
function getFileIcon(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  const iconMap = {
    txt: '📄',
    md: '📝',
    pdf: '📕',
    doc: '📘',
    docx: '📘',
    pptx: '📙',
    xlsx: '📊',
    csv: '📊',
    json: '📋',
    xml: '📋',
    html: '🌐',
    mp3: '🎵',
    wav: '🎵',
    flac: '🎵',
    ogg: '🎵',
    zip: '📦',
    png: '🖼️',
    jpg: '🖼️',
    jpeg: '🖼️',
    gif: '🖼️',
    webp: '🖼️'
  };
  return iconMap[ext] || '📎';
}

// Format file size
function formatFileSize(bytes) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i];
}

// Remove a file from the list
function removeFile(index) {
  uploadedFiles.splice(index, 1);
  updateFilesList();
}

// Clear all files
function clearAllFiles() {
  uploadedFiles = [];
  updateFilesList();
}

// Get uploaded files for sending with message
function getUploadedFiles() {
  return uploadedFiles;
}

// Clear files after sending
function clearFilesAfterSend() {
  uploadedFiles = [];
  updateFilesList();
}

// Add a file to upload list (used by sessions to attach stored files)
function addFileToUpload(fileData) {
  const filesArea = document.getElementById('uploadedFilesArea');
  if (!filesArea) {
    console.error('Chat upload area not found - make sure chat is visible');
    return false;
  }

  uploadedFiles.push({
    name: fileData.name,
    type: fileData.type,
    size: fileData.size,
    content: fileData.content,
    encoding: inferContentEncoding(fileData)
  });
  updateFilesList();

  // Scroll to input area to show the attached file
  const inputContainer = document.getElementById('inputContainer');
  if (inputContainer) {
    inputContainer.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }

  return true;
}

// Make functions globally available
window.initFileUpload = initFileUpload;
window.getUploadedFiles = getUploadedFiles;
window.clearFilesAfterSend = clearFilesAfterSend;
window.addFileToUpload = addFileToUpload;
window.getAttachedChatNotes = getAttachedChatNotes;
window.clearAttachedNotesAfterSend = clearAttachedNotesAfterSend;

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initFileUpload);
} else {
  initFileUpload();
}

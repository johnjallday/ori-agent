// File Upload Module
// Handles file uploads and attachment management for chat

let uploadedFiles = [];

// Initialize file upload functionality
function initFileUpload() {
  const fileInput = document.getElementById('fileUpload');
  const clearFilesBtn = document.getElementById('clearFilesBtn');
  const directoryBtn = document.getElementById('attachDirectoryBtn');

  if (fileInput) {
    fileInput.addEventListener('change', handleFileSelect);
  }

  if (clearFilesBtn) {
    clearFilesBtn.addEventListener('click', clearAllFiles);
  }

  if (directoryBtn) {
    directoryBtn.addEventListener('click', openFolderPickerForChat);
  }

  // Initialize drag and drop
  initDragAndDrop();
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
  document.addEventListener('dragover', (e) => e.preventDefault());
  document.addEventListener('drop', (e) => e.preventDefault());

  // Listen on chat container for drag events
  if (chatContainer) {
    chatContainer.addEventListener('dragenter', (e) => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        dragCounter++;
        showDropZone();
      }
    });

    chatContainer.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        e.dataTransfer.dropEffect = 'copy';
      }
    });

    chatContainer.addEventListener('dragleave', (e) => {
      e.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) {
        hideDropZone();
      }
    });

    chatContainer.addEventListener('drop', async (e) => {
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
    inputContainer.addEventListener('dragenter', (e) => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        dragCounter++;
        showDropZone();
      }
    });

    inputContainer.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer.types.includes('Files')) {
        e.dataTransfer.dropEffect = 'copy';
      }
    });

    inputContainer.addEventListener('dragleave', (e) => {
      e.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) {
        hideDropZone();
      }
    });

    inputContainer.addEventListener('drop', async (e) => {
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
  if (type.startsWith('text/') || type.includes('json') || type.includes('xml') || type.includes('csv') ||
      type.includes('markdown') || type.includes('html')) {
    return 'text';
  }
  return isLikelyBase64(fileData.content) ? 'base64' : 'text';
}

// Process files (shared between file input and drag-drop)
async function processFiles(files) {
  // Allowed file extensions
  const allowedExtensions = ['txt', 'md', 'pdf', 'doc', 'docx', 'csv', 'json', 'xml', 'html', 'mp3', 'wav', 'flac', 'ogg', 'zip', 'pptx', 'xlsx', 'png', 'jpg', 'jpeg', 'gif', 'webp'];

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
          'pdf': 'application/pdf',
          'wav': 'audio/wav',
          'mp3': 'audio/mpeg',
          'aiff': 'audio/aiff',
          'aif': 'audio/aiff',
          'flac': 'audio/flac',
          'ogg': 'audio/ogg',
          'mid': 'audio/midi',
          'midi': 'audio/midi',
          'zip': 'application/zip',
          'pptx': 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
          'xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
          'png': 'image/png',
          'jpg': 'image/jpeg',
          'jpeg': 'image/jpeg',
          'gif': 'image/gif',
          'webp': 'image/webp'
        };
        mimeType = mimeMap[ext] || 'application/octet-stream';
      }

      uploadedFiles.push({
        name: file.name,
        type: mimeType,
        size: file.size,
        content: result.binaryContent || result.content,  // Prefer binary content for files that have it
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
  return ['pdf', 'docx', 'doc', 'pptx', 'xlsx', 'wav', 'mp3', 'aiff', 'aif', 'flac', 'ogg', 'mid', 'midi', 'zip', 'png', 'jpg', 'jpeg', 'gif', 'webp'].includes(ext);
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

    reader.onload = async (e) => {
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

    reader.onerror = (e) => {
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
      <button class="btn-remove-file" data-index="${index}" style="background: none; border: none; color: var(--text-muted); cursor: pointer; padding: 0; margin-left: 4px; font-size: 14px;">×</button>
    `;

    filesList.appendChild(fileChip);
  });

  // Add click handlers for remove buttons
  document.querySelectorAll('.btn-remove-file').forEach(btn => {
    btn.addEventListener('click', (e) => {
      const index = parseInt(e.target.dataset.index);
      removeFile(index);
    });
  });
}

// Get file icon based on file extension
function getFileIcon(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  const iconMap = {
    'txt': '📄',
    'md': '📝',
    'pdf': '📕',
    'doc': '📘',
    'docx': '📘',
    'pptx': '📙',
    'xlsx': '📊',
    'csv': '📊',
    'json': '📋',
    'xml': '📋',
    'html': '🌐',
    'mp3': '🎵',
    'wav': '🎵',
    'flac': '🎵',
    'ogg': '🎵',
    'zip': '📦',
    'png': '🖼️',
    'jpg': '🖼️',
    'jpeg': '🖼️',
    'gif': '🖼️',
    'webp': '🖼️'
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

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initFileUpload);
} else {
  initFileUpload();
}

// File Upload Module
// Handles file uploads and attachment management for chat

let uploadedFiles = [];

// Initialize file upload functionality
function initFileUpload() {
  const fileInput = document.getElementById('fileUpload');
  const clearFilesBtn = document.getElementById('clearFilesBtn');

  if (fileInput) {
    fileInput.addEventListener('change', handleFileSelect);
  }

  if (clearFilesBtn) {
    clearFilesBtn.addEventListener('click', clearAllFiles);
  }
}

// Handle file selection
async function handleFileSelect(event) {
  const files = Array.from(event.target.files);

  for (const file of files) {
    // Check file size (max 10MB)
    if (file.size > 10 * 1024 * 1024) {
      alert(`File ${file.name} is too large. Maximum size is 10MB.`);
      continue;
    }

    try {
      const result = await readFileContent(file);

      // Determine MIME type - use actual type or infer from extension
      let mimeType = file.type;
      if (!mimeType) {
        const ext = file.name.split('.').pop().toLowerCase();
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
          'zip': 'application/zip'
        };
        mimeType = mimeMap[ext] || 'application/octet-stream';
      }

      uploadedFiles.push({
        name: file.name,
        type: mimeType,
        size: file.size,
        content: result.binaryContent || result.content  // Prefer binary content for files that have it
      });

      console.log(`File added: ${file.name}, type: ${mimeType}, has binary: ${!!result.binaryContent}`);
    } catch (error) {
      console.error(`Error reading file ${file.name}:`, error);
      alert(`Failed to read file ${file.name}`);
    }
  }

  // Clear the input so the same file can be selected again
  event.target.value = '';

  updateFilesList();
}

// Check if file is binary (PDF, DOCX, DOC, audio, etc.)
function isBinaryFile(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  return ['pdf', 'docx', 'doc', 'wav', 'mp3', 'aiff', 'aif', 'flac', 'ogg', 'mid', 'midi', 'zip'].includes(ext);
}

// Check if file should be parsed for text (for LLM consumption)
function shouldParseForText(filename) {
  const ext = filename.split('.').pop().toLowerCase();
  return ['pdf', 'docx', 'doc'].includes(ext);
}

// Read file content - returns { content, binaryContent } for binary files
async function readFileContent(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();

    reader.onload = async (e) => {
      let content = e.target.result;

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
                'Content-Type': 'application/json',
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
    'csv': '📊',
    'json': '📋',
    'xml': '📋',
    'html': '🌐'
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

// Make functions globally available
window.initFileUpload = initFileUpload;
window.getUploadedFiles = getUploadedFiles;
window.clearFilesAfterSend = clearFilesAfterSend;

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initFileUpload);
} else {
  initFileUpload();
}

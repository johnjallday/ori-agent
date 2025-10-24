// Dolphin Agent Application JavaScript

let currentAgent = '';
let isComposing = false; // IME safety
let isWaitingForResponse = false; // Chat state

// Prompt history for up/down arrow navigation
let promptHistory = [];
let historyIndex = -1;

// Chat messages storage
let chatMessages = [];

// ---- Dark Mode (Bootstrap + custom) ----
function applyTheme(isDark) {
  // Bootstrap theming
  document.documentElement.setAttribute('data-bs-theme', isDark ? 'dark' : 'light');
  // Your extra overrides
  document.documentElement.classList.toggle('dark-mode', isDark);
  // Persist
  localStorage.setItem('darkMode', String(isDark));
}

// Setup dark mode functionality
function setupDarkMode() {
  // Init theme from storage (default light)
  const storedDark = localStorage.getItem('darkMode') === 'true';
  applyTheme(storedDark);

  // Toggle button
  const darkModeToggle = document.getElementById('darkModeToggle');
  if (darkModeToggle) {
    darkModeToggle.addEventListener('click', () => {
      const next = !(localStorage.getItem('darkMode') === 'true');
      applyTheme(next);
    });
  }
}

// ---- Agent Display Functionality ----

// Refresh agent display in navbar
async function refreshAgentDisplay() {
  try {
    const response = await fetch('/api/agents');
    if (response.ok) {
      const data = await response.json();
      const currentAgentElement = document.querySelector('#currentAgentDisplay span.fw-medium');

      if (currentAgentElement && data.current) {
        currentAgentElement.textContent = data.current;
      }
    }
  } catch (error) {
    console.error('Failed to refresh agent display:', error);
  }
}

// ---- Chat Functionality ----

// Render structured result based on displayType
function renderStructuredResult(structuredData) {
  const { displayType, title, description, data, metadata } = structuredData;

  let html = '';

  // Add title if present
  if (title) {
    html += `<h5 style="margin-bottom: 8px;">${escapeHtml(title)}</h5>`;
  }

  // Add description if present
  if (description) {
    html += `<p style="margin-bottom: 12px; color: var(--text-secondary);">${escapeHtml(description)}</p>`;
  }

  switch (displayType) {
    case 'table':
      html += renderTable(data, metadata);
      break;
    case 'modal':
      html += renderModal(data, metadata);
      break;
    case 'list':
      html += renderList(data);
      break;
    case 'card':
      html += renderCards(data);
      break;
    case 'json':
      html += renderJSON(data);
      break;
    case 'text':
    default:
      html += escapeHtml(typeof data === 'string' ? data : JSON.stringify(data));
  }

  return html;
}

// Render table display
function renderTable(data, metadata) {
  if (!Array.isArray(data) || data.length === 0) {
    return '<p>No data to display</p>';
  }

  const columns = metadata?.columns || Object.keys(data[0]);

  let html = '<div style="overflow-x: auto;"><table class="table table-sm table-bordered table-hover" style="margin-top: 8px;">';

  // Table header
  html += '<thead class="table-light"><tr>';
  columns.forEach(col => {
    html += `<th style="padding: 10px; font-weight: 600;">${escapeHtml(col)}</th>`;
  });
  html += '</tr></thead>';

  // Table body
  html += '<tbody>';
  data.forEach(row => {
    html += '<tr>';
    columns.forEach(col => {
      const key = col.toLowerCase();
      const value = row[key];
      let displayValue = '';

      if (value === null || value === undefined) {
        displayValue = '-';
      } else if (typeof value === 'object') {
        displayValue = JSON.stringify(value);
      } else {
        displayValue = String(value);
      }

      html += `<td style="padding: 10px;">${escapeHtml(displayValue)}</td>`;
    });
    html += '</tr>';
  });
  html += '</tbody></table></div>';

  return html;
}

// Render modal display (interactive modal with buttons/actions)
function renderModal(data, metadata) {
  if (!Array.isArray(data) || data.length === 0) {
    return '<p>No items available</p>';
  }

  const modalId = 'modal-' + Date.now();
  const buttonLabel = metadata?.buttonLabel || 'Select';
  const operation = metadata?.operation || '';
  const action = metadata?.action || '';

  let html = `
    <div class="modal-script-selector" id="${modalId}">
      <div class="list-group mb-3" style="max-height: 400px; overflow-y: auto;">
        ${data.map((item, index) => `
          <label class="list-group-item list-group-item-action d-flex align-items-start" style="cursor: pointer;">
            <input type="checkbox" name="script-select-${modalId}" value="${index}" class="me-3 mt-1 form-check-input" data-filename="${escapeHtml(item.filename || '')}" data-item-index="${index}">
            <div class="flex-grow-1">
              <div class="d-flex w-100 justify-content-between">
                <h6 class="mb-1">${escapeHtml(item.name || item.title || `Item ${index + 1}`)}</h6>
                ${item.size ? `<small class="text-muted">${escapeHtml(item.size)}</small>` : ''}
              </div>
              ${item.description ? `<p class="mb-1 small text-muted">${escapeHtml(item.description)}</p>` : ''}
              ${item.filename ? `<small class="text-primary">📄 ${escapeHtml(item.filename)}</small>` : ''}
            </div>
          </label>
        `).join('')}
      </div>
      <div class="d-flex justify-content-between align-items-center">
        <span class="text-muted small" id="selected-count-${modalId}">0 selected</span>
        <div class="d-flex gap-2">
          <button type="button" class="btn btn-secondary btn-sm" id="select-all-btn-${modalId}">Select All</button>
          <button type="button" class="btn btn-secondary btn-sm" id="clear-btn-${modalId}">Clear All</button>
          <button type="button" class="btn btn-primary" id="download-btn-${modalId}">
            <span class="download-icon">⬇️</span> ${escapeHtml(buttonLabel)}
          </button>
        </div>
      </div>
    </div>
  `;

  // Add click handlers after rendering
  setTimeout(() => {
    const checkboxes = document.querySelectorAll(`input[name="script-select-${modalId}"]`);
    const selectedCount = document.getElementById(`selected-count-${modalId}`);
    const selectAllBtn = document.getElementById(`select-all-btn-${modalId}`);
    const clearBtn = document.getElementById(`clear-btn-${modalId}`);
    const downloadBtn = document.getElementById(`download-btn-${modalId}`);

    // Update selected count
    function updateCount() {
      const checked = document.querySelectorAll(`input[name="script-select-${modalId}"]:checked`).length;
      if (selectedCount) {
        selectedCount.textContent = `${checked} selected`;
      }
    }

    // Add change listener to all checkboxes
    checkboxes.forEach(cb => {
      cb.addEventListener('change', updateCount);
    });

    // Select All button
    if (selectAllBtn) {
      selectAllBtn.addEventListener('click', function() {
        checkboxes.forEach(cb => cb.checked = true);
        updateCount();
      });
    }

    // Clear All button
    if (clearBtn) {
      clearBtn.addEventListener('click', function() {
        checkboxes.forEach(cb => cb.checked = false);
        updateCount();
      });
    }

    // Download button
    if (downloadBtn) {
      downloadBtn.addEventListener('click', async function() {
        const selected = document.querySelectorAll(`input[name="script-select-${modalId}"]:checked`);
        if (selected.length === 0) {
          alert('Please select at least one script');
          return;
        }

        // Get all selected filenames
        const filenames = Array.from(selected).map(cb => {
          const index = parseInt(cb.value);
          const item = data[index];
          return item.filename || item.name;
        });

        // Disable button and show loading
        downloadBtn.disabled = true;
        downloadBtn.innerHTML = `<span class="spinner-border spinner-border-sm me-2"></span>Downloading ${filenames.length} script(s)...`;

        let successCount = 0;
        let errorCount = 0;

        try {
          // Download each script sequentially using direct API call
          for (const filename of filenames) {
            try {
              const response = await fetch('/api/plugins/tool-call', {
                method: 'POST',
                headers: {
                  'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                  plugin_name: "dolphin-reaper",
                  operation: "download_script",
                  args: {
                    filename: filename
                  }
                })
              });

              if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
              }

              const result = await response.json();

              if (result.success) {
                successCount++;
                addMessageToChat(result.result, false);
              } else {
                errorCount++;
                addMessageToChat(`Error downloading ${filename}: ${result.error}`, false, true);
              }
            } catch (error) {
              console.error(`Error downloading ${filename}:`, error);
              errorCount++;
              addMessageToChat(`Error downloading ${filename}: ${error.message}`, false, true);
            }
          }

          // Show summary
          if (successCount > 0) {
            downloadBtn.innerHTML = `<span class="download-icon">✅</span> Downloaded ${successCount}!`;
          } else {
            downloadBtn.innerHTML = `<span class="download-icon">❌</span> All failed`;
          }

          setTimeout(() => {
            downloadBtn.innerHTML = `<span class="download-icon">⬇️</span> ${escapeHtml(buttonLabel)}`;
            downloadBtn.disabled = false;
            // Uncheck all checkboxes
            selected.forEach(cb => cb.checked = false);
            updateCount();
          }, 2000);

        } catch (error) {
          console.error('Download error:', error);
          addMessageToChat(`Error: ${error.message}`, false, true);
          downloadBtn.innerHTML = `<span class="download-icon">⬇️</span> ${escapeHtml(buttonLabel)}`;
          downloadBtn.disabled = false;
        }
      });
    }
  }, 100);

  return html;
}

// Render list display
function renderList(data) {
  if (!Array.isArray(data)) {
    data = [data];
  }

  let html = '<ul class="list-unstyled">';
  data.forEach(item => {
    if (typeof item === 'object') {
      html += `<li style="padding: 6px 0;">• ${escapeHtml(item.name || item.title || JSON.stringify(item))}</li>`;
    } else {
      html += `<li style="padding: 6px 0;">• ${escapeHtml(String(item))}</li>`;
    }
  });
  html += '</ul>';

  return html;
}

// Render cards display
function renderCards(data) {
  if (!Array.isArray(data)) {
    data = [data];
  }

  let html = '<div class="row g-3">';
  data.forEach(item => {
    html += `
      <div class="col-md-6 col-lg-4">
        <div class="card">
          <div class="card-body">
            <h6 class="card-title">${escapeHtml(item.title || item.name || 'Card')}</h6>
            ${item.description ? `<p class="card-text small">${escapeHtml(item.description)}</p>` : ''}
          </div>
        </div>
      </div>
    `;
  });
  html += '</div>';

  return html;
}

// Render JSON display
function renderJSON(data) {
  const jsonStr = JSON.stringify(data, null, 2);
  return `<pre style="background: var(--bg-hover); padding: 12px; border-radius: 4px; overflow-x: auto;"><code>${escapeHtml(jsonStr)}</code></pre>`;
}

// Try to parse and render JSON as a table (legacy support)
function tryRenderJsonTable(message) {
  // First, try to parse as structured result
  try {
    const structuredData = JSON.parse(message);
    if (structuredData.displayType && structuredData.data) {
      return renderStructuredResult(structuredData);
    }
  } catch (e) {
    // Not a structured result, continue with legacy parsing
  }

  // Extract JSON from message (handle case where message contains both text and JSON)
  const jsonMatch = message.match(/(\[[\s\S]*\]|\{[\s\S]*\})/);
  if (!jsonMatch) return null;

  try {
    const jsonData = JSON.parse(jsonMatch[0]);

    // Check if it's a structured result
    if (jsonData.displayType && jsonData.data) {
      return renderStructuredResult(jsonData);
    }

    // Check if it's an array of objects (legacy)
    if (Array.isArray(jsonData) && jsonData.length > 0 && typeof jsonData[0] === 'object') {
      // Get prefix text (text before JSON)
      const prefixText = message.substring(0, jsonMatch.index).trim();

      // Extract all unique keys from the objects
      const allKeys = new Set();
      jsonData.forEach(obj => {
        Object.keys(obj).forEach(key => allKeys.add(key));
      });
      const keys = Array.from(allKeys);

      // Build HTML table (legacy rendering)
      let html = '';
      if (prefixText) {
        html += `<div style="margin-bottom: 12px;">${escapeHtml(prefixText)}</div>`;
      }

      html += renderTable(jsonData, { columns: keys });

      return html;
    }

    return null;
  } catch (e) {
    return null;
  }
}

// HTML escape helper function
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// Add message to chat area
function addMessageToChat(message, isUser = false, isError = false) {
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const messageDiv = document.createElement('div');
  messageDiv.className = `message-container mb-3 ${isUser ? 'user-message' : 'assistant-message'}`;
  
  const messageContent = document.createElement('div');
  messageContent.className = `modern-card p-3 ${isUser ? 'ms-auto' : 'me-auto'}`;
  messageContent.style.maxWidth = '85%';
  
  if (isError) {
    messageContent.style.background = 'var(--danger-color)';
    messageContent.style.color = 'white';
  } else if (isUser) {
    messageContent.style.background = 'var(--primary-color)';
    messageContent.style.color = 'white';
  } else {
    messageContent.style.background = 'var(--bg-secondary)';
    messageContent.style.color = 'var(--text-primary)';
  }

  // Process message content (support markdown and JSON tables)
  if (!isUser) {
    // Try to detect and render JSON tables
    const tableContent = tryRenderJsonTable(message);
    if (tableContent) {
      messageContent.innerHTML = tableContent;
    } else if (typeof marked !== 'undefined') {
      messageContent.innerHTML = marked.parse(message);
    } else {
      messageContent.textContent = message;
    }
  } else {
    messageContent.textContent = message;
  }

  messageDiv.appendChild(messageContent);
  chatArea.appendChild(messageDiv);
  
  // Scroll to bottom
  chatArea.scrollTop = chatArea.scrollHeight;
  
  // Store message
  chatMessages.push({
    content: message,
    isUser: isUser,
    timestamp: new Date().toISOString()
  });
}

// Show typing indicator
function showTypingIndicator() {
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const typingDiv = document.createElement('div');
  typingDiv.id = 'typingIndicator';
  typingDiv.className = 'message-container mb-3 assistant-message';
  
  const typingContent = document.createElement('div');
  typingContent.className = 'modern-card p-3 me-auto';
  typingContent.style.maxWidth = '85%';
  typingContent.style.background = 'var(--bg-secondary)';
  typingContent.innerHTML = `
    <div class="d-flex align-items-center">
      <span style="margin-right: 8px;">Assistant is typing</span>
      <div class="typing-dots">
        <span></span><span></span><span></span>
      </div>
    </div>
  `;
  
  typingDiv.appendChild(typingContent);
  chatArea.appendChild(typingDiv);
  chatArea.scrollTop = chatArea.scrollHeight;
}

// Hide typing indicator
function hideTypingIndicator() {
  const typingIndicator = document.getElementById('typingIndicator');
  if (typingIndicator) {
    typingIndicator.remove();
  }
}

// Send message to chat API
async function sendMessage(message) {
  if (isWaitingForResponse) return;

  const trimmedMessage = message.trim();
  if (!trimmedMessage) return;

  // Add to history
  promptHistory.unshift(trimmedMessage);
  historyIndex = -1;

  // Get uploaded files
  const uploadedFiles = window.getUploadedFiles ? window.getUploadedFiles() : [];

  // Add user message to chat (including file info if any)
  let displayMessage = trimmedMessage;
  if (uploadedFiles.length > 0) {
    const fileNames = uploadedFiles.map(f => f.name).join(', ');
    displayMessage += `\n\n📎 Attached: ${fileNames}`;
  }
  addMessageToChat(displayMessage, true);

  // Clear input
  const input = document.getElementById('input');
  if (input) {
    input.value = '';
    input.style.height = 'auto';
  }

  // Set loading state
  isWaitingForResponse = true;
  updateSendButton();
  showTypingIndicator();

  try {
    // Prepare request body with files
    const requestBody = {
      question: trimmedMessage
    };

    // Add files if any
    if (uploadedFiles.length > 0) {
      requestBody.files = uploadedFiles;
    }

    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(requestBody)
    });

    hideTypingIndicator();

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();

    console.log('Received data:', data);
    console.log('data.response:', data.response);
    console.log('typeof data.response:', typeof data.response);

    // Clear uploaded files after successful send
    if (window.clearFilesAfterSend) {
      window.clearFilesAfterSend();
    }

    if (data.response) {
      addMessageToChat(data.response, false);

      // Check if this was a successful /switch command and refresh agent display and sidebar
      console.log('Checking for switch command:', {
        message: trimmedMessage,
        startsWithSwitch: trimmedMessage.startsWith('/switch'),
        hasCheckmark: data.response.includes('✅'),
        hasSwitched: data.response.includes('Switched to agent'),
        response: data.response
      });

      if (trimmedMessage.startsWith('/switch') && data.response.includes('✅') && data.response.includes('Switched to agent')) {
        console.log('Successful agent switch detected, refreshing agent display and sidebar');
        setTimeout(() => {
          refreshAgentDisplay();
          // Refresh sidebar agents list if the function exists
          if (typeof loadAgents === 'function') {
            loadAgents();
          }
        }, 100); // Small delay to ensure backend has updated
      }
    } else {
      console.error('No response field found. Available fields:', Object.keys(data));
      addMessageToChat('Sorry, I received an unexpected response format.', false, true);
    }

  } catch (error) {
    console.error('Chat error:', error);
    hideTypingIndicator();
    addMessageToChat(`Error: ${error.message}`, false, true);
  } finally {
    isWaitingForResponse = false;
    updateSendButton();
  }
}

// Update send button state
function updateSendButton() {
  const sendBtn = document.getElementById('sendBtn');
  if (!sendBtn) return;

  if (isWaitingForResponse) {
    sendBtn.textContent = 'Sending...';
    sendBtn.disabled = true;
    sendBtn.style.opacity = '0.6';
  } else {
    sendBtn.textContent = 'Send';
    sendBtn.disabled = false;
    sendBtn.style.opacity = '1';
  }
}

// Setup chat event listeners
function setupChat() {
  const input = document.getElementById('input');
  const sendBtn = document.getElementById('sendBtn');
  const enterToSend = document.getElementById('enterToSend');

  if (!input || !sendBtn) {
    console.warn('Chat elements not found');
    return;
  }

  // Send button click
  sendBtn.addEventListener('click', () => {
    const message = input.value.trim();
    if (message && !isWaitingForResponse) {
      sendMessage(message);
    }
  });

  // Input handling
  input.addEventListener('keydown', (e) => {
    if (isComposing) return;

    // Handle Enter key
    if (e.key === 'Enter') {
      const shouldSend = enterToSend ? enterToSend.checked : true;
      
      if (shouldSend && !e.shiftKey) {
        e.preventDefault();
        const message = input.value.trim();
        if (message && !isWaitingForResponse) {
          sendMessage(message);
        }
      }
    }
    
    // Handle history navigation
    if (e.key === 'ArrowUp' && !e.shiftKey && promptHistory.length > 0) {
      e.preventDefault();
      if (historyIndex < promptHistory.length - 1) {
        historyIndex++;
        input.value = promptHistory[historyIndex];
      }
    }
    
    if (e.key === 'ArrowDown' && !e.shiftKey) {
      e.preventDefault();
      if (historyIndex > 0) {
        historyIndex--;
        input.value = promptHistory[historyIndex];
      } else if (historyIndex === 0) {
        historyIndex = -1;
        input.value = '';
      }
    }
  });

  // IME composition handling
  input.addEventListener('compositionstart', () => {
    isComposing = true;
  });

  input.addEventListener('compositionend', () => {
    isComposing = false;
  });

  // Auto-resize textarea
  input.addEventListener('input', () => {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
  });

  // Enter to send toggle
  if (enterToSend) {
    enterToSend.addEventListener('change', () => {
      localStorage.setItem('enterToSend', enterToSend.checked);
    });
    
    // Load saved preference
    const savedEnterToSend = localStorage.getItem('enterToSend');
    if (savedEnterToSend !== null) {
      enterToSend.checked = savedEnterToSend === 'true';
    }
  }

  console.log('Chat functionality initialized');
}

// ---- Sidebar Functionality ----
// Sidebar functionality has been moved to modular files:
// - js/modules/agents.js - Agent management
// - js/modules/plugins.js - Plugin management  
// - js/modules/settings.js - Settings management
// - js/modules/sidebar.js - Main sidebar controller

// ---- Sidebar Toggle Functionality ----
function setupSidebarToggle() {
  const sidebarToggle = document.getElementById('sidebarToggle');
  const sidebar = document.getElementById('sidebar');

  if (sidebarToggle && sidebar) {
    sidebarToggle.addEventListener('click', function() {
      // Toggle sidebar visibility
      sidebar.classList.toggle('d-none');

      // Toggle sidebar position for mobile overlay
      if (sidebar.classList.contains('d-none')) {
        sidebar.classList.remove('sidebar-mobile-show');
      } else {
        sidebar.classList.add('sidebar-mobile-show');
      }
    });

    // Close sidebar when clicking outside on mobile
    document.addEventListener('click', function(event) {
      const isClickInSidebar = sidebar.contains(event.target);
      const isClickOnToggle = sidebarToggle.contains(event.target);

      // Only close if sidebar is visible and click is outside
      if (!isClickInSidebar && !isClickOnToggle &&
          !sidebar.classList.contains('d-none') &&
          window.innerWidth < 992) { // lg breakpoint
        sidebar.classList.add('d-none');
        sidebar.classList.remove('sidebar-mobile-show');
      }
    });

    // Handle window resize
    window.addEventListener('resize', function() {
      if (window.innerWidth >= 992) { // lg breakpoint
        // Show sidebar on large screens
        sidebar.classList.remove('d-none');
        sidebar.classList.remove('sidebar-mobile-show');
        sidebar.classList.add('d-lg-block');
      } else {
        // Hide sidebar on small screens by default
        sidebar.classList.add('d-none');
        sidebar.classList.remove('d-lg-block');
        sidebar.classList.remove('sidebar-mobile-show');
      }
    });
  }
}

// Initialize application
async function initializeApp() {
  // Set up dark mode functionality
  setupDarkMode();

  // Set up chat functionality
  setupChat();

  // Set up sidebar toggle functionality
  setupSidebarToggle();

  // Initialize onboarding for first-time users
  try {
    const { onboardingManager } = await import('./modules/onboarding.js');
    await onboardingManager.init();
  } catch (error) {
    console.error('Failed to initialize onboarding:', error);
  }

  // Sidebar functionality is now handled by modular files

  console.log('App initialized successfully');
}

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', function() {
  initializeApp();
});

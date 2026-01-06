// Ori Agent Application JavaScript

let currentAgent = '';
let isComposing = false; // IME safety
// Note: Chat state is now managed by chatStateMachine (see modules/chat-state.js)

// Prompt history for up/down arrow navigation
let promptHistory = [];
let historyIndex = -1;

// Chat messages storage
let chatMessages = [];

// Remove stored slash-command exchanges and system announcements from history
function sanitizeHistory(messages) {
  const cleaned = [];
  let skipNextAssistant = false;

  for (let i = 0; i < messages.length; i++) {
    const msg = messages[i];
    const content = (msg && msg.content) ? String(msg.content) : '';

    if (!msg || !content) {
      continue;
    }

    // Remove update-available announcements that may have been persisted in older sessions
    const isUpdateAnnouncement =
      content.includes('Update Available') &&
      content.includes('Latest Version:');

    if (isUpdateAnnouncement) {
      continue;
    }

    if (skipNextAssistant) {
      // Skip the assistant reply immediately following a slash command
      if (!msg.isUser) {
        skipNextAssistant = false;
        continue;
      }
      // If the next message is also user (edge case), keep evaluating
      skipNextAssistant = false;
    }

    if (msg.isUser && content.trim().startsWith('/')) {
      // Skip the slash command and mark to skip its direct reply
      skipNextAssistant = true;
      continue;
    }

    cleaned.push(msg);
  }

  return cleaned;
}

// ---- Chat Persistence (localStorage) ----

// Save chat messages to localStorage for current agent
function saveChatToLocalStorage() {
  if (!currentAgent) return;

  try {
    const storageKey = `ori_chat_${currentAgent}`;
    const sanitized = sanitizeHistory(chatMessages);
    localStorage.setItem(storageKey, JSON.stringify(sanitized));
  } catch (error) {
    console.error('Failed to save chat history:', error);
    // Silent fail - don't show toast for localStorage issues
  }
}

// Load chat messages from localStorage for current agent
function loadChatFromLocalStorage() {
  if (!currentAgent) return;

  try {
    const storageKey = `ori_chat_${currentAgent}`;
    const stored = localStorage.getItem(storageKey);

    if (stored) {
      chatMessages = sanitizeHistory(JSON.parse(stored));
      // Persist the cleaned history so old slash entries are removed
      saveChatToLocalStorage();
      // Restore messages to UI
      restoreChatMessages();
    }
  } catch (error) {
    console.error('Failed to load chat history:', error);
    chatMessages = [];
    // Silent fail - don't show toast for localStorage issues
  }
}

// Restore chat messages to the UI
function restoreChatMessages() {
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  // Clear existing messages in UI
  chatArea.innerHTML = '';

  // Re-render all stored messages
  chatMessages.forEach(msg => {
    appendMessageToUI(msg.content, msg.isUser);
  });
}

// Clear chat history for current agent
function clearChatHistory() {
  if (!currentAgent) return;

  try {
    const storageKey = `ori_chat_${currentAgent}`;
    localStorage.removeItem(storageKey);
    chatMessages = [];

    // Clear UI
    const chatArea = document.getElementById('chatArea');
    if (chatArea) {
      chatArea.innerHTML = '';
    }

    console.log('Chat history cleared');
    if (window.Toast) {
      Toast.success('Chat history cleared');
    }
  } catch (error) {
    console.error('Failed to clear chat history:', error);
    if (window.Toast) {
      Toast.error('Failed to clear chat history');
    }
  }
}

// ---- Agent Display Functionality ----

// Setup click handler for agent display in navbar
function setupAgentDisplayClick() {
  const agentDisplay = document.getElementById('currentAgentDisplay');
  if (!agentDisplay) return;

  // Make it look clickable
  agentDisplay.style.cursor = 'pointer';
  agentDisplay.title = 'Click to view agent details';

  agentDisplay.addEventListener('click', function() {
    const agentNameSpan = this.querySelector('.fw-medium');
    const agentName = agentNameSpan?.textContent?.trim();

    if (agentName) {
      window.location.href = `/agents/${encodeURIComponent(agentName)}`;
    }
  });
}

// Refresh agent display in navbar
async function refreshAgentDisplay() {
  try {
    const response = await fetch('/api/agents');
    if (response.ok) {
      const data = await response.json();
      const currentAgentElement = document.querySelector('#currentAgentDisplay span.fw-medium');

      if (currentAgentElement && data.current) {
        currentAgentElement.textContent = data.current;

        // Update current agent and load chat history when agent changes
        const previousAgent = currentAgent;
        currentAgent = data.current;

        // Load chat history if agent changed
        if (previousAgent !== currentAgent) {
          loadChatFromLocalStorage();
        }
      }
    }
  } catch (error) {
    console.error('Failed to refresh agent display:', error);
    if (window.Toast) {
      Toast.error('Failed to load agent information');
    }
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
                  plugin_name: "ori-reaper",
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
              if (window.Toast) {
                Toast.error(`Failed to download ${filename}`);
              }
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
          if (window.Toast) {
            Toast.error('Download failed');
          }
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

// Internal function: Add message to UI only (used by restore function)
function appendMessageToUI(message, isUser = false, isError = false) {
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

  // Smart scroll - only if user hasn't scrolled up to read history
  if (window.scrollChatToBottomIfNeeded) {
    window.scrollChatToBottomIfNeeded(isUser); // Force scroll for user messages
  } else {
    // Fallback to simple scroll if module not loaded
    chatArea.scrollTop = chatArea.scrollHeight;
  }
}

// Public function: Add message and optionally persist to localStorage
function addMessageToChat(message, isUser = false, isError = false, isSystemNotification = false, skipHistory = false) {
  // Add to UI
  appendMessageToUI(message, isUser, isError);

  // Skip storing system notifications or intentionally non-persisted messages (e.g., slash commands)
  // Only store actual user queries and assistant responses that should be remembered
  if (isSystemNotification || skipHistory) {
    return;
  }

  // Store message in memory
  chatMessages.push({
    content: message,
    isUser: isUser,
    timestamp: new Date().toISOString()
  });

  // Persist to localStorage
  saveChatToLocalStorage();
}

function formatToolCallsForChat(toolCalls) {
  if (!Array.isArray(toolCalls) || toolCalls.length === 0) return '';

  // Render as markdown with collapsible details (works with marked)
  return toolCalls
    .map((tc, idx) => {
      const functionName = tc.function || tc.name || `tool_${idx + 1}`;
      const args = typeof tc.args === 'string' ? tc.args : JSON.stringify(tc.args ?? {}, null, 2);
      const result = typeof tc.result === 'string' ? tc.result : JSON.stringify(tc.result ?? '', null, 2);
      return [
        `<details>`,
        `<summary>Tool: <code>${functionName}</code></summary>`,
        `<div style="margin-top:8px">`,
        `<div><strong>Args</strong></div>`,
        `<pre style="white-space:pre-wrap; margin:8px 0;">${escapeHtml(args)}</pre>`,
        `<div><strong>Result</strong></div>`,
        `<pre style="white-space:pre-wrap; margin:8px 0;">${escapeHtml(result)}</pre>`,
        `</div>`,
        `</details>`
      ].join('\n');
    })
    .join('\n\n');
}

function escapeHtml(text) {
  return String(text)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

// Chat state machine reference (initialized in initializeApp)
let chatStateMachine = null;

// Send message to chat API
async function sendMessage(message) {
  // Check if state machine is active (replaces isWaitingForResponse)
  if (chatStateMachine && chatStateMachine.isActive()) return;

  let trimmedMessage = message.trim();
  if (!trimmedMessage) return;

  // Expand @notename references if sessionManager is available
  if (window.sessionManager?.expandNoteReferences) {
    trimmedMessage = await window.sessionManager.expandNoteReferences(trimmedMessage);
  }

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
  const isSlashCommand = trimmedMessage.startsWith('/');
  addMessageToChat(displayMessage, true, false, false, isSlashCommand);

  // Clear input
  const input = document.getElementById('input');
  if (input) {
    input.value = '';
    input.style.height = 'auto';
  }

  // Start state machine - shows "Sending..." indicator
  if (chatStateMachine) {
    chatStateMachine.send();
  }
  updateSendButton();

  try {
    // Prepare request body with files
    const requestBody = {
      question: trimmedMessage
    };

    // Add files if any
    if (uploadedFiles.length > 0) {
      requestBody.files = uploadedFiles;
    }

    // Add agent_name from the active session (for per-session agent binding)
    const activeSession = window.sessionManager?.getActiveSession?.();
    if (activeSession?.agent_name) {
      requestBody.agent_name = activeSession.agent_name;
    }

    // Build headers with session ID for multi-tab support
    const chatHeaders = {
      'Content-Type': 'application/json',
    };
    if (window.sessionManager && window.sessionManager.getActiveSessionId()) {
      chatHeaders['X-Session-ID'] = window.sessionManager.getActiveSessionId();
    }

    const fetchOptions = {
      method: 'POST',
      headers: chatHeaders,
      body: JSON.stringify(requestBody)
    };

    // Add abort signal for cancel functionality
    if (chatStateMachine && chatStateMachine.getSignal()) {
      fetchOptions.signal = chatStateMachine.getSignal();
    }

    const response = await fetch('/api/chat', fetchOptions);

    // Transition to thinking state while processing response
    // Check isActive() to avoid race condition if user cancelled during fetch
    if (chatStateMachine && chatStateMachine.isActive()) {
      chatStateMachine.think();
    }

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

    // Transition to processing state while formatting response
    // Check isActive() to avoid race condition if user cancelled
    if (chatStateMachine && chatStateMachine.isActive()) {
      chatStateMachine.process();
    }

    const hasResponseField = Object.prototype.hasOwnProperty.call(data, 'response');
    const responseText = typeof data.response === 'string' ? data.response : null;
    const toolCallsText = formatToolCallsForChat(data.toolCalls);

    if (hasResponseField && responseText !== null) {
      // Skip persisting assistant replies for slash commands
      if (responseText.trim().length > 0) {
        addMessageToChat(responseText, false, false, false, isSlashCommand);
      } else if (toolCallsText) {
        addMessageToChat(toolCallsText, false, false, false, isSlashCommand);
      } else {
        addMessageToChat('(no text response)', false, false, false, isSlashCommand);
      }

      // Check if this was a successful /switch command and refresh agent display and sidebar
      console.log('Checking for switch command:', {
        message: trimmedMessage,
        startsWithSwitch: trimmedMessage.startsWith('/switch'),
        hasCheckmark: responseText.includes('✅'),
        hasSwitched: responseText.includes('Switched to agent'),
        response: responseText
      });

      if (trimmedMessage.startsWith('/switch') && responseText.includes('✅') && responseText.includes('Switched to agent')) {
        console.log('Successful agent switch detected, refreshing agent display and sidebar');
        setTimeout(() => {
          refreshAgentDisplay();
          // Refresh sidebar agents list if the function exists
          if (typeof loadAgents === 'function') {
            loadAgents();
          }
        }, 100); // Small delay to ensure backend has updated
      }

      // Notify session manager about the new message
      if (window.sessionManager && window.sessionManager.onMessageSent) {
        window.sessionManager.onMessageSent();
      }
    } else {
      console.error('No response field found. Available fields:', Object.keys(data));
      const details = escapeHtml(JSON.stringify(data, null, 2));
      addMessageToChat(`Sorry, I received an unexpected response format.\n\n<details><summary>Raw response</summary><pre style="white-space:pre-wrap; margin:8px 0;">${details}</pre></details>`, false, true);
      if (window.Toast) {
        Toast.warning('Received unexpected response format');
      }
    }

  } catch (error) {
    // Handle user cancellation gracefully
    if (error.name === 'AbortError') {
      console.log('Request cancelled by user');
      return;
    }

    console.error('Chat error:', error);
    addMessageToChat(`Error: ${error.message}`, false, true, false, isSlashCommand);
    if (window.Toast) {
      Toast.error('Failed to send message');
    }
  } finally {
    // Complete state machine (returns to idle, hides indicator)
    if (chatStateMachine) {
      chatStateMachine.complete();
    }
    updateSendButton();
  }
}

// Update send button state based on chat state machine
function updateSendButton() {
  const sendBtn = document.getElementById('sendBtn');
  if (!sendBtn) return;

  const isActive = chatStateMachine && chatStateMachine.isActive();

  sendBtn.disabled = isActive;
  sendBtn.style.opacity = isActive ? '0.6' : '1';
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
    const isActive = chatStateMachine && chatStateMachine.isActive();
    if (message && !isActive) {
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
        const isActive = chatStateMachine && chatStateMachine.isActive();
        if (message && !isActive) {
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

  // Clear chat button
  const clearChatBtn = document.getElementById('clearChatBtn');
  if (clearChatBtn) {
    clearChatBtn.addEventListener('click', () => {
      if (confirm('Are you sure you want to clear the chat history? This cannot be undone.')) {
        clearChatHistory();
      }
    });
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
    const isEditableTarget = (target) => {
      if (!target) return false;
      const tagName = target.tagName;
      return target.isContentEditable || tagName === 'INPUT' || tagName === 'TEXTAREA' || tagName === 'SELECT';
    };

    sidebarToggle.addEventListener('click', function(event) {
      console.log('[SIDEBAR TOGGLE] Click detected');
      console.log('[SIDEBAR TOGGLE] Event target:', event.target);
      console.log('[SIDEBAR TOGGLE] Current sidebar classes:', sidebar.className);

      // Prevent event propagation to avoid interference with other handlers
      event.stopPropagation();

      // Toggle sidebar visibility
      const isHidden = sidebar.classList.toggle('d-none');

      if (isHidden) {
        // Hiding sidebar
        sidebar.classList.remove('d-lg-block');
        sidebar.classList.remove('sidebar-mobile-show');
        sidebarToggle.setAttribute('aria-expanded', 'false');
      } else {
        // Showing sidebar
        if (window.innerWidth >= 992) {
          sidebar.classList.add('d-lg-block');
          sidebar.classList.remove('sidebar-mobile-show');
        } else {
          // On mobile, add sidebar-mobile-show to override transform: translateX(-100%)
          sidebar.classList.remove('d-lg-block');
          sidebar.classList.add('sidebar-mobile-show');
        }
        sidebarToggle.setAttribute('aria-expanded', 'true');
      }

      console.log('[SIDEBAR TOGGLE] New sidebar classes:', sidebar.className);
      console.log('[SIDEBAR TOGGLE] Sidebar hidden?', sidebar.classList.contains('d-none'));

      // Handle sidebar width
      if (isHidden) {
        // Set sidebar width to 0 to remove the empty space
        document.documentElement.style.setProperty('--sidebar-width', '0px');
      } else {
        // Restore sidebar width (get from resizer or use default)
        const savedWidth = localStorage.getItem('sidebarWidth') || '300';
        document.documentElement.style.setProperty('--sidebar-width', `${savedWidth}px`);
      }
    });

    document.addEventListener('keydown', (event) => {
      if (event.metaKey && event.code === 'Backquote' && !event.shiftKey && !event.ctrlKey && !event.altKey) {
        if (isEditableTarget(event.target)) {
          return;
        }
        event.preventDefault();
        sidebarToggle.click();
      }
    });

    // Close sidebar when clicking outside on mobile
    document.addEventListener('click', function(event) {
      const isClickInSidebar = sidebar.contains(event.target);
      const isClickOnToggle = sidebarToggle.contains(event.target);
      // Don't close sidebar when clicking on modals or their backdrops
      const isClickInModal = event.target.closest('.modal') || event.target.classList.contains('modal-backdrop');

      // Only close if sidebar is visible and click is outside (excluding modals)
      if (!isClickInSidebar && !isClickOnToggle && !isClickInModal &&
          !sidebar.classList.contains('d-none') &&
          window.innerWidth < 992) { // lg breakpoint
        sidebar.classList.add('d-none');
        sidebar.classList.remove('sidebar-mobile-show');
        sidebar.classList.remove('d-lg-block');
        // Set sidebar width to 0 to remove the empty space
        document.documentElement.style.setProperty('--sidebar-width', '0px');
      }
    });

    // Handle window resize
    function handleSidebarResponsive() {
      if (window.innerWidth >= 992) { // lg breakpoint
        // Show sidebar on large screens
        sidebar.classList.remove('d-none');
        sidebar.classList.remove('sidebar-mobile-show');
        sidebar.classList.add('d-lg-block');
        // Restore sidebar width
        const savedWidth = localStorage.getItem('sidebarWidth') || '300';
        document.documentElement.style.setProperty('--sidebar-width', `${savedWidth}px`);
      } else {
        // Hide sidebar on small screens by default
        sidebar.classList.add('d-none');
        sidebar.classList.remove('d-lg-block');
        sidebar.classList.remove('sidebar-mobile-show');
        // Set sidebar width to 0
        document.documentElement.style.setProperty('--sidebar-width', '0px');
      }
    }

    window.addEventListener('resize', handleSidebarResponsive);

    // Run initial check on page load
    handleSidebarResponsive();
  }
}

// Export functions for use by session manager
window.appendMessageToUI = appendMessageToUI;
window.clearChatHistory = clearChatHistory;

// Initialize application
async function initializeApp() {
  // Initialize chat state machine
  try {
    const chatStateModule = await import('./modules/chat-state.js');
    const chatStateUIModule = await import('./modules/chat-state-ui.js');

    chatStateMachine = chatStateModule.chatStateMachine;
    chatStateUIModule.initChatStateUI();

    // Clean up on page unload to prevent memory leaks
    window.addEventListener('beforeunload', () => {
      if (chatStateMachine) {
        chatStateMachine.cancel();
      }
      chatStateUIModule.cleanupChatStateUI();
    });

    console.log('Chat state machine initialized');
  } catch (error) {
    console.error('Failed to initialize chat state machine:', error);
    // Fall back to simple behavior without state machine
  }

  // Initialize plugin initialization banner
  try {
    const pluginBannerModule = await import('./modules/plugin-init-banner.js');
    await pluginBannerModule.initPluginBanner();
    console.log('Plugin init banner initialized');
  } catch (error) {
    console.error('Failed to initialize plugin banner:', error);
  }

  // Initialize chat auto-scroll
  try {
    const autoScrollModule = await import('./modules/chat-auto-scroll.js');
    autoScrollModule.initChatAutoScroll();

    // Clean up on page unload
    window.addEventListener('beforeunload', () => {
      autoScrollModule.cleanupChatAutoScroll();
    });

    console.log('Chat auto-scroll initialized');
  } catch (error) {
    console.error('Failed to initialize auto-scroll:', error);
  }

  // Set up chat functionality
  setupChat();

  // Set up sidebar toggle functionality
  setupSidebarToggle();

  // Set up agent display click handler (navigate to agent details)
  setupAgentDisplayClick();

  // Load current agent and restore chat history
  await refreshAgentDisplay();

  // Initialize onboarding for first-time users
  try {
    const { onboardingManager } = await import('./modules/onboarding.js');
    await onboardingManager.init();
  } catch (error) {
    console.error('Failed to initialize onboarding:', error);
    // Silent fail - onboarding is optional
  }

  // Sidebar functionality is now handled by modular files

  console.log('App initialized successfully');
}

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', function() {
  initializeApp();
});

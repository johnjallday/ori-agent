// Ori Agent Application JavaScript

const appLog = Logger.withContext('App');

let isComposing = false; // IME safety
// Note: Chat state is now managed by chatStateMachine (see modules/chat-state.js)

// Prompt history for up/down arrow navigation
const promptHistory = [];
let historyIndex = -1;

// Chat messages storage
let chatMessages = [];
let chatWebSearchToggleRequestId = 0;
const PLAN_BEFORE_ACTION_STORAGE_KEY = 'planBeforeAction';
const pendingActionPlanContexts = new Map();
let pendingChatRouteContext = null;
const CHAT_SPECIALIST_INTENT_DEFAULTS = {
  utility_direct: {
    defaultType: 'general',
    suggestedName: 'Utility Assistant',
    tags: ['utility', 'time', 'weather', 'facts'],
    domains: ['utility'],
    externalSystems: [],
    sideEffects: 'none',
    descriptionBase:
      'Create a utility assistant for quick everyday requests such as time lookups, weather checks, simple conversions, and short factual questions.',
    systemPrompt:
      'You are a utility assistant for quick requests. Handle time, weather, simple conversions, and short factual lookups with concise direct answers.'
  },
  travel_planning: {
    defaultType: 'research',
    suggestedName: 'Travel Planner',
    tags: ['travel', 'itinerary', 'planning'],
    domains: ['travel'],
    externalSystems: [],
    sideEffects: 'none',
    descriptionBase:
      'Create an agent that plans multi-day travel itineraries with day-by-day plans, transportation ideas, budget ranges, and local recommendations.',
    systemPrompt:
      'You are a travel planning assistant. Build realistic day-by-day itineraries with concise options, practical transit notes, and budget-aware recommendations.'
  },
  email_check: {
    defaultType: 'tool-calling',
    suggestedName: 'Email Assistant',
    tags: ['email', 'inbox', 'communication'],
    domains: ['email'],
    externalSystems: ['email', 'gmail', 'outlook'],
    sideEffects: 'external_account',
    descriptionBase:
      'Create an email triage agent that summarizes unread mail, categorizes urgency, and drafts replies. It must default to read-only behavior and never send without explicit user confirmation.',
    systemPrompt:
      'You are an email assistant. Summarize inbox content and draft responses. Never send or delete email without explicit user approval. Start in read-only mode.'
  },
  calendar_check: {
    defaultType: 'tool-calling',
    suggestedName: 'Calendar Assistant',
    tags: ['calendar', 'schedule', 'planning'],
    domains: ['calendar'],
    externalSystems: ['calendar'],
    sideEffects: 'external_account',
    descriptionBase:
      'Create a calendar assistant that checks schedule availability, summarizes upcoming events, and answers calendar questions. It must default to read-only behavior and always use configured skills or MCP connectors before claiming lack of access.',
    systemPrompt:
      'You are a calendar assistant. Default to read-only behavior unless the user explicitly asks to create or edit events.'
  },
  app_launch: {
    defaultType: 'tool-calling',
    suggestedName: 'Desktop Launcher',
    tags: ['desktop', 'automation', 'apps'],
    domains: ['desktop'],
    externalSystems: [],
    sideEffects: 'local_app',
    descriptionBase:
      'Create a desktop launcher agent that can interpret app-launch requests, execute safe local launch commands, and confirm completion clearly.',
    systemPrompt:
      'You are a desktop app launcher assistant. For requests like "open obsidian", launch the requested app immediately and confirm success or report the exact failure reason.'
  },
  general_task: {
    defaultType: 'general',
    suggestedName: 'Task Assistant',
    tags: ['tasks', 'assistant'],
    domains: ['tasks'],
    externalSystems: [],
    sideEffects: '',
    descriptionBase:
      'Create a practical task execution assistant that can route and complete user requests from chat.',
    systemPrompt:
      'You are a helpful assistant focused on completing practical user tasks with clear, concise outputs.'
  }
};

// Remove stored slash-command exchanges and system announcements from history
function sanitizeHistory(messages) {
  const cleaned = [];
  let skipNextAssistant = false;

  for (let i = 0; i < messages.length; i++) {
    const msg = messages[i];
    const content = msg && msg.content ? String(msg.content) : '';

    if (!msg || !content) {
      continue;
    }

    // Remove update-available announcements that may have been persisted in older sessions
    const isUpdateAnnouncement =
      content.includes('Update Available') && content.includes('Latest Version:');

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

function getDefaultAssistantAgentName() {
  // The protected system assistant, under the one identity Issue #350 settled
  // on. Must match internal/systemassistant's canonical name.
  return 'Ask Ori';
}

function getActiveChatStorageKey() {
  const activeSessionId = window.sessionManager?.getActiveSessionId?.();
  if (activeSessionId) {
    return `ori_chat_session_${activeSessionId}`;
  }

  const pathname = String(window.location?.pathname || '/').trim() || '/';
  const workspaceId =
    extractWorkspaceIdFromPath(pathname) ||
    String(window.sessionManager?.getActiveSession?.()?.folder_id || '').trim();
  if (workspaceId) {
    return `ori_chat_assistant_${workspaceId}`;
  }

  return 'ori_chat_assistant';
}

// Save chat messages to localStorage for the active Assistant thread/session.
function saveChatToLocalStorage() {
  try {
    const storageKey = getActiveChatStorageKey();
    const sanitized = sanitizeHistory(chatMessages);
    localStorage.setItem(storageKey, JSON.stringify(sanitized));
  } catch (error) {
    appLog.error('Failed to save chat history:', error);
    // Silent fail - don't show toast for localStorage issues
  }
}

// Load chat messages from localStorage for the active Assistant thread/session.
function loadChatFromLocalStorage() {
  try {
    const storageKey = getActiveChatStorageKey();
    const stored = localStorage.getItem(storageKey);

    if (stored) {
      chatMessages = sanitizeHistory(JSON.parse(stored));
      // Persist the cleaned history so old slash entries are removed
      saveChatToLocalStorage();
      // Restore messages to UI
      restoreChatMessages();
    }
  } catch (error) {
    appLog.error('Failed to load chat history:', error);
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
    appendMessageToUI(msg.content, msg.isUser, false, msg.routeMeta || null);
  });
}

function replaceChatHistoryMessages(messages) {
  if (!Array.isArray(messages)) {
    chatMessages = [];
  } else {
    chatMessages = sanitizeHistory(messages);
  }
  saveChatToLocalStorage();
}

// Clear chat history for the active Assistant thread/session.
function clearChatHistory() {
  try {
    const storageKey = getActiveChatStorageKey();
    localStorage.removeItem(storageKey);
    chatMessages = [];

    // Clear UI
    const chatArea = document.getElementById('chatArea');
    if (chatArea) {
      chatArea.innerHTML = '';
    }

    appLog.info('Chat history cleared');
    if (window.Toast) {
      Toast.success('Chat history cleared');
    }
  } catch (error) {
    appLog.error('Failed to clear chat history:', error);
    if (window.Toast) {
      Toast.error('Failed to clear chat history');
    }
  }
}

function resolveChatWebSearchAgentName(explicitAgentName = '') {
  const direct = String(explicitAgentName || '').trim();
  if (direct) return direct;

  const sessionAgent = window.sessionManager?.getActiveSession?.()?.agent_name;
  if (sessionAgent) return String(sessionAgent).trim();

  return getDefaultAssistantAgentName();
}

function setChatWebSearchToggleVisualState({ enabled, loading, agentName, available }) {
  const btn = document.getElementById('chatWebSearchToggleBtn');
  const label = document.getElementById('chatWebSearchToggleLabel');
  if (!btn || !label) return;

  const hasAgent = Boolean(agentName && String(agentName).trim());
  const isAvailable = available !== false;
  const allow = enabled !== false;

  btn.classList.remove('btn-outline-secondary', 'btn-outline-success', 'btn-outline-danger');
  if (!isAvailable || !hasAgent) {
    btn.classList.add('btn-outline-secondary');
  } else if (allow) {
    btn.classList.add('btn-outline-success');
  } else {
    btn.classList.add('btn-outline-danger');
  }

  if (!isAvailable || !hasAgent) {
    label.textContent = 'Web: --';
  } else if (loading) {
    label.textContent = 'Web: ...';
  } else {
    label.textContent = allow ? 'Web: On' : 'Web: Off';
  }

  btn.dataset.agentName = hasAgent ? String(agentName).trim() : '';
  if (isAvailable && hasAgent) {
    btn.dataset.enabled = allow ? 'true' : 'false';
  } else {
    btn.dataset.enabled = '';
  }
  btn.disabled = loading || !isAvailable || !hasAgent;
}

async function refreshChatWebSearchToggle(explicitAgentName = '') {
  const btn = document.getElementById('chatWebSearchToggleBtn');
  if (!btn) return;

  const agentName = resolveChatWebSearchAgentName(explicitAgentName);
  if (!agentName) {
    setChatWebSearchToggleVisualState({
      enabled: true,
      loading: false,
      agentName: '',
      available: false
    });
    return;
  }

  const requestId = ++chatWebSearchToggleRequestId;
  setChatWebSearchToggleVisualState({ enabled: true, loading: true, agentName, available: true });

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    if (requestId !== chatWebSearchToggleRequestId) return;

    const allowWebSearch =
      typeof data.allow_web_search === 'boolean' ? data.allow_web_search : true;
    setChatWebSearchToggleVisualState({
      enabled: allowWebSearch,
      loading: false,
      agentName,
      available: true
    });
  } catch (error) {
    appLog.warn('Failed to load chat web search toggle state', error);
    if (requestId !== chatWebSearchToggleRequestId) return;
    setChatWebSearchToggleVisualState({
      enabled: true,
      loading: false,
      agentName,
      available: false
    });
  }
}

async function toggleChatWebSearchForActiveAgent() {
  const btn = document.getElementById('chatWebSearchToggleBtn');
  if (!btn) return;

  const agentName = resolveChatWebSearchAgentName(btn.dataset.agentName || '');
  if (!agentName) {
    if (window.Toast) {
      Toast.warning('No active agent selected.');
    }
    return;
  }

  const currentEnabled = btn.dataset.enabled !== 'false';
  const nextEnabled = !currentEnabled;
  setChatWebSearchToggleVisualState({
    enabled: currentEnabled,
    loading: true,
    agentName,
    available: true
  });

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        allow_web_search: nextEnabled
      })
    });
    if (!response.ok) {
      let errorMessage = `Failed to update web toggle (HTTP ${response.status})`;
      try {
        const errorData = await response.json();
        if (errorData?.error) errorMessage = errorData.error;
      } catch {
        // Ignore response parse errors.
      }
      throw new Error(errorMessage);
    }

    await refreshChatWebSearchToggle(agentName);

    if (window.Toast) {
      Toast.success(nextEnabled ? 'Web tools enabled.' : 'Web tools disabled.');
    }
  } catch (error) {
    appLog.error('Failed to toggle chat web search', error);
    if (window.Toast) {
      Toast.error(error.message || 'Failed to update web tool setting');
    }
    await refreshChatWebSearchToggle(agentName);
  }
}

function setupChatWebSearchToggle() {
  const btn = document.getElementById('chatWebSearchToggleBtn');
  if (!btn) return;
  if (btn.dataset.bound === 'true') return;
  btn.dataset.bound = 'true';

  btn.addEventListener('click', async event => {
    event.preventDefault();
    await toggleChatWebSearchForActiveAgent();
  });

  refreshChatWebSearchToggle();
}

window.refreshChatWebSearchToggle = refreshChatWebSearchToggle;
window.refreshAgentDisplay = refreshAgentDisplay;

// ---- Agent Display Functionality ----

// Setup click handler for agent display in navbar
function setupAgentDisplayClick() {
  const agentDisplay = document.getElementById('currentAgentDisplay');
  if (!agentDisplay) return;

  const updateDisplayState = () => {
    const agentName = String(agentDisplay.dataset.agentName || '').trim();
    if (agentName) {
      agentDisplay.style.cursor = 'pointer';
      agentDisplay.title = 'Click to view agent details';
      return;
    }
    agentDisplay.style.cursor = 'default';
    agentDisplay.title = 'Assistant';
  };

  agentDisplay.addEventListener('click', function () {
    const agentName = String(this.dataset.agentName || '').trim();
    if (agentName) {
      window.location.href = `/agents/${encodeURIComponent(agentName)}`;
    }
  });

  updateDisplayState();
}

// Refresh the Assistant/execution-agent display in the navbar.
async function refreshAgentDisplay() {
  const currentAgentElement = document.querySelector('#currentAgentDisplay span.fw-medium');
  const agentDisplay = document.getElementById('currentAgentDisplay');
  const sessionAgent = String(window.sessionManager?.getActiveSession?.()?.agent_name || '').trim();
  const displayName = sessionAgent || 'Assistant';

  if (currentAgentElement) {
    currentAgentElement.textContent = displayName;
  }
  if (agentDisplay) {
    agentDisplay.dataset.agentName = sessionAgent;
    agentDisplay.style.cursor = sessionAgent ? 'pointer' : 'default';
    agentDisplay.title = sessionAgent ? 'Click to view agent details' : 'Assistant';
  }

  loadChatFromLocalStorage();
  refreshChatWebSearchToggle(sessionAgent || getDefaultAssistantAgentName());
}

// ---- System Model Display ----

let systemModelDisplayRequestId = 0;

async function fetchSystemModelStatus() {
  const options = { cache: 'no-store' };
  let timeoutId = null;
  let controller = null;

  if (typeof AbortController !== 'undefined') {
    controller = new AbortController();
    options.signal = controller.signal;
    timeoutId = window.setTimeout(() => {
      controller.abort();
    }, 8000);
  }

  try {
    return await fetch('/api/settings/system-model', options);
  } finally {
    if (timeoutId !== null) {
      window.clearTimeout(timeoutId);
    }
  }
}

// Fetch and display system model in navbar
async function refreshSystemModelDisplay() {
  const modelNameEl = document.getElementById('systemModelName');
  const providerEl = document.getElementById('navSystemModelProvider');
  const indicatorEl = document.getElementById('systemModelIndicator');

  if (!modelNameEl || !providerEl) return;

  const requestId = ++systemModelDisplayRequestId;

  try {
    const response = await fetchSystemModelStatus();
    if (!response.ok) {
      throw new Error('Failed to fetch system model');
    }
    const data = await response.json();
    if (requestId !== systemModelDisplayRequestId) return;

    if (data.configured && data.model) {
      // Display model name (truncate if too long)
      const modelName = data.model.length > 20 ? data.model.substring(0, 18) + '...' : data.model;
      modelNameEl.textContent = modelName;
      modelNameEl.title = data.model; // Full name on hover

      // Display provider badge
      if (data.provider) {
        providerEl.textContent = data.provider;
        providerEl.style.display = 'inline';

        // Color-code by provider
        switch (data.provider.toLowerCase()) {
          case 'openai':
            providerEl.style.background = 'rgba(16, 163, 127, 0.2)';
            providerEl.style.color = '#10a37f';
            break;
          case 'claude':
          case 'anthropic':
            providerEl.style.background = 'rgba(204, 147, 102, 0.2)';
            providerEl.style.color = '#cc9366';
            break;
          case 'gemini':
            providerEl.style.background = 'rgba(66, 133, 244, 0.2)';
            providerEl.style.color = '#4285f4';
            break;
          case 'ollama':
            providerEl.style.background = 'rgba(59, 130, 246, 0.2)';
            providerEl.style.color = '#3b82f6';
            break;
          case 'lmstudio':
            providerEl.style.background = 'rgba(14, 165, 233, 0.2)';
            providerEl.style.color = '#0ea5e9';
            break;
          case 'mlx_lm':
            providerEl.style.background = 'rgba(249, 115, 22, 0.2)';
            providerEl.style.color = '#f97316';
            break;
          default:
            providerEl.style.background = 'var(--bg-tertiary)';
            providerEl.style.color = 'var(--text-muted)';
        }
      } else {
        providerEl.style.display = 'none';
      }

      if (indicatorEl) {
        indicatorEl.title = `System Model: ${data.model} (${data.provider}) - Click to configure`;
      }
    } else {
      modelNameEl.textContent = 'Not configured';
      providerEl.style.display = 'none';
      if (indicatorEl) {
        indicatorEl.title = 'System Model not configured - Click to set up';
      }
    }

    EventBus.emit('systemModel:loaded', data);
  } catch (error) {
    if (requestId !== systemModelDisplayRequestId) return;
    appLog.error('Failed to load system model:', error);
    modelNameEl.textContent = 'Error';
    providerEl.style.display = 'none';
  }
}

window.refreshSystemModelDisplay = refreshSystemModelDisplay;

// Listen for settings changes to update the model display
EventBus.on('settings:saved', () => {
  refreshSystemModelDisplay();
});

EventBus.on('systemModel:changed', () => {
  refreshSystemModelDisplay();
});

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

  let html =
    '<div style="overflow-x: auto;"><table class="table table-sm table-bordered table-hover" style="margin-top: 8px;">';

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

  const html = `
    <div class="modal-script-selector" id="${modalId}">
      <div class="list-group mb-3" style="max-height: 400px; overflow-y: auto;">
        ${data
          .map(
            (item, index) => `
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
        `
          )
          .join('')}
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
      const checked = document.querySelectorAll(
        `input[name="script-select-${modalId}"]:checked`
      ).length;
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
      selectAllBtn.addEventListener('click', function () {
        checkboxes.forEach(cb => (cb.checked = true));
        updateCount();
      });
    }

    // Clear All button
    if (clearBtn) {
      clearBtn.addEventListener('click', function () {
        checkboxes.forEach(cb => (cb.checked = false));
        updateCount();
      });
    }

    // Download button
    if (downloadBtn) {
      downloadBtn.addEventListener('click', async function () {
        const selected = document.querySelectorAll(
          `input[name="script-select-${modalId}"]:checked`
        );
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
        let _errorCount = 0;

        try {
          // Download each script sequentially using direct API call
          for (const filename of filenames) {
            try {
              const result = await API.post('/api/plugins/tool-call', {
                plugin_name: 'ori-reaper',
                operation: 'download_script',
                args: {
                  filename: filename
                }
              });

              if (result.success) {
                successCount++;
                addMessageToChat(result.result, false);
              } else {
                _errorCount++;
                addMessageToChat(`Error downloading ${filename}: ${result.error}`, false, true);
              }
            } catch (error) {
              appLog.error(`Error downloading ${filename}:`, error);
              _errorCount++;
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
            selected.forEach(cb => (cb.checked = false));
            updateCount();
          }, 2000);
        } catch (error) {
          appLog.error('Download error:', error);
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

// Internal function: Add message to UI only (used by restore function)
function normalizeRouteMetadata(payload) {
  if (!payload || typeof payload !== 'object') return null;

  const route = payload.route && typeof payload.route === 'object' ? payload.route : {};
  const mode = String(route.mode || payload.route_mode || '')
    .trim()
    .toLowerCase();
  if (!mode) return null;

  const toolName = String(route.tool_name || payload.tool_name || '').trim();
  const provider = String(route.provider || payload.provider || '').trim();
  const parsedToolCount = Number(route.tool_count || payload.tool_count || 0);
  const toolCount =
    Number.isFinite(parsedToolCount) && parsedToolCount > 0 ? Math.floor(parsedToolCount) : 0;

  return {
    mode,
    toolName,
    provider,
    toolCount
  };
}

function normalizeSpecialistToken(value) {
  return String(value || '')
    .trim()
    .toLowerCase();
}

function uniqueSpecialistValues(values) {
  const seen = Object.create(null);
  const out = [];
  for (const value of Array.isArray(values) ? values : []) {
    const trimmed = String(value || '').trim();
    if (!trimmed || seen[trimmed]) continue;
    seen[trimmed] = true;
    out.push(trimmed);
  }
  return out;
}

function getChatSpecialistIntentDefaults(intentKey) {
  const normalizedKey = normalizeSpecialistToken(intentKey);
  return (
    CHAT_SPECIALIST_INTENT_DEFAULTS[normalizedKey] || CHAT_SPECIALIST_INTENT_DEFAULTS.general_task
  );
}

function normalizeSpecialistHandoff(payload) {
  if (!payload || typeof payload !== 'object') return null;

  const handoff =
    payload.specialist_handoff && typeof payload.specialist_handoff === 'object'
      ? payload.specialist_handoff
      : payload;
  const mode = String(handoff.route_mode || payload.route_mode || '')
    .trim()
    .toLowerCase();
  if (mode !== 'specialist_handoff') return null;

  const matchedAgent = String(handoff.matched_agent || payload.matched_agent || '').trim();
  return {
    mode,
    matchedAgent,
    requiresCreation: handoff.requires_creation === true || payload.requires_creation === true,
    routingPolicy: String(handoff.routing_policy || payload.routing_policy || '')
      .trim()
      .toLowerCase(),
    intent: String(handoff.intent || payload.intent || '').trim(),
    intentLabel: String(handoff.intent_label || payload.intent_label || '').trim(),
    suggestedAgentName: String(
      handoff.suggested_agent_name || payload.suggested_agent_name || ''
    ).trim(),
    suggestedAgentType: String(
      handoff.suggested_agent_type || payload.suggested_agent_type || ''
    ).trim()
  };
}

function buildChatSpecialistDescription(originalMessage, handoff) {
  const defaults = getChatSpecialistIntentDefaults(handoff && handoff.intent);
  const taskText = String(originalMessage || '').trim();
  return `${defaults.descriptionBase} User task: "${taskText}".`;
}

function buildChatSpecialistSystemPrompt(handoff) {
  return getChatSpecialistIntentDefaults(handoff && handoff.intent).systemPrompt;
}

function buildChatSpecialistTags(handoff) {
  const defaults = getChatSpecialistIntentDefaults(handoff && handoff.intent);
  return uniqueSpecialistValues((defaults.tags || []).concat(['auto-created', 'chat-handoff']));
}

function buildChatSpecialistRoutingProfile(originalMessage, handoff) {
  const defaults = getChatSpecialistIntentDefaults(handoff && handoff.intent);
  const taskText = String(originalMessage || '').trim();
  return {
    match_phrases: taskText ? [taskText] : [],
    example_requests: taskText ? [taskText] : [],
    domains: Array.isArray(defaults.domains) ? defaults.domains.slice() : [],
    external_systems: Array.isArray(defaults.externalSystems)
      ? defaults.externalSystems.slice()
      : [],
    side_effects: defaults.sideEffects || ''
  };
}

function buildUniqueChatSpecialistAgentName(baseName, existingNames) {
  let sanitized = String(baseName || 'Task Assistant')
    .replace(/[^a-zA-Z0-9 _-]/g, '')
    .trim();
  if (!sanitized) sanitized = 'Task Assistant';

  const lowerNames = Object.create(null);
  for (const existingName of Array.isArray(existingNames) ? existingNames : []) {
    lowerNames[normalizeSpecialistToken(existingName)] = true;
  }

  if (!lowerNames[normalizeSpecialistToken(sanitized)]) {
    return sanitized;
  }

  for (let suffix = 2; suffix <= 99; suffix += 1) {
    const candidate = `${sanitized} ${suffix}`;
    if (!lowerNames[normalizeSpecialistToken(candidate)]) {
      return candidate;
    }
  }

  return `${sanitized} ${Date.now()}`;
}

async function fetchChatSpecialistCreationAvailability() {
  try {
    const response = await fetch('/api/agents/auto-config/availability');
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    return {
      available: Boolean(data && data.available),
      systemModelConfigured: Boolean(data && data.system_model_configured),
      message: String((data && data.message) || '')
    };
  } catch (error) {
    appLog.warn('Failed to load specialist creation availability', error);
    return {
      available: false,
      systemModelConfigured: false,
      message: ''
    };
  }
}

function buildChatSpecialistAvailabilityMessage(availability) {
  if (!availability || availability.systemModelConfigured !== true) {
    return 'A System Model must be configured before I can create this specialist. Open Settings to configure it.';
  }
  return 'No suitable LLM provider is available right now. Open Settings to configure a provider or model before creating this specialist.';
}

async function maybeLoadChatSpecialistAutoConfig(description) {
  try {
    const response = await fetch('/api/agents/auto-config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ description })
    });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    return await response.json();
  } catch (error) {
    appLog.warn('Failed to auto-configure specialist agent, using fallback defaults', error);
    return null;
  }
}

async function fetchExistingChatAgentNames() {
  const response = await fetch('/api/agents');
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`);
  }
  const data = await response.json();
  const agents = Array.isArray(data && data.agents) ? data.agents : [];
  return agents
    .map(agent => (typeof agent === 'string' ? agent : agent && agent.name))
    .filter(Boolean);
}

async function createSpecialistAgentFromHandoff(payload, originalMessage) {
  const handoff = normalizeSpecialistHandoff(payload);
  if (!handoff || !handoff.requiresCreation) {
    throw new Error('No pending specialist creation was found.');
  }

  const availability = await fetchChatSpecialistCreationAvailability();
  if (!availability.available) {
    throw new Error(buildChatSpecialistAvailabilityMessage(availability));
  }

  const defaults = getChatSpecialistIntentDefaults(handoff.intent);
  const description = buildChatSpecialistDescription(originalMessage, handoff);
  const autoConfig = await maybeLoadChatSpecialistAutoConfig(description);
  const existingNames = await fetchExistingChatAgentNames();
  const desiredBaseName =
    autoConfig && autoConfig.agent_name
      ? autoConfig.agent_name
      : handoff.suggestedAgentName || defaults.suggestedName;
  const agentName = buildUniqueChatSpecialistAgentName(desiredBaseName, existingNames);

  const requestBody = {
    name: agentName,
    type:
      (autoConfig && autoConfig.agent_type) ||
      handoff.suggestedAgentType ||
      defaults.defaultType ||
      'tool-calling',
    system_prompt:
      (autoConfig && autoConfig.system_prompt) || buildChatSpecialistSystemPrompt(handoff),
    description: (autoConfig && autoConfig.description) || description,
    tags: buildChatSpecialistTags(handoff),
    routing_profile: buildChatSpecialistRoutingProfile(originalMessage, handoff)
  };
  if (autoConfig && typeof autoConfig.model === 'string' && autoConfig.model.trim()) {
    requestBody.model = autoConfig.model.trim();
  }
  if (autoConfig && typeof autoConfig.temperature === 'number') {
    requestBody.temperature = autoConfig.temperature;
  }

  const response = await fetch('/api/agents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(requestBody)
  });
  if (!response.ok) {
    let message = `Failed to create specialist (HTTP ${response.status})`;
    try {
      const errorData = await response.json();
      if (errorData && errorData.error) {
        message = errorData.error;
      }
    } catch {
      // Ignore response parse errors.
    }
    throw new Error(message);
  }

  return agentName;
}

function buildDisplayMessageForRequest(requestBody, fallbackMessage) {
  const question =
    typeof requestBody?.question === 'string' ? requestBody.question : fallbackMessage;
  let displayMessage = String(question || '').trim();
  const files = Array.isArray(requestBody?.files) ? requestBody.files : [];
  if (files.length > 0) {
    const fileNames = files
      .map(file => file && file.name)
      .filter(Boolean)
      .join(', ');
    if (fileNames) {
      displayMessage += `\n\n📎 Attached: ${fileNames}`;
    }
  }
  return displayMessage;
}

async function createSpecialistSessionForHandoff(agentName, requestBody) {
  const manager = window.sessionManager;
  if (!manager || !agentName) return null;

  const workspaceId = String(
    requestBody?.route_context?.workspace_id || manager.getActiveSession?.()?.folder_id || ''
  ).trim();

  if (workspaceId && typeof manager.createSessionWithAgentInFolder === 'function') {
    const createdInWorkspace = await manager.createSessionWithAgentInFolder(agentName, workspaceId);
    if (createdInWorkspace && createdInWorkspace.id) {
      return createdInWorkspace;
    }
    const activeWorkspaceSession = manager.getActiveSession?.();
    if (
      activeWorkspaceSession &&
      activeWorkspaceSession.id &&
      activeWorkspaceSession.agent_name === agentName
    ) {
      return activeWorkspaceSession;
    }
  }

  if (typeof manager.createSessionWithAgent === 'function') {
    const created = await manager.createSessionWithAgent(agentName);
    if (created && created.id) {
      return created;
    }
  }

  const activeSession = manager.getActiveSession?.() || null;
  if (activeSession && activeSession.agent_name === agentName) {
    return activeSession;
  }
  return null;
}

async function executeSpecialistHandoff(payload, fallbackMessage, isSlashCommand) {
  const handoff = normalizeSpecialistHandoff(payload);
  if (!handoff || !handoff.matchedAgent || handoff.requiresCreation) {
    return false;
  }

  const requestBody =
    payload && typeof payload._requestBody === 'object' ? { ...payload._requestBody } : null;
  if (!requestBody) return false;

  const requestHeaders =
    payload && typeof payload._requestHeaders === 'object'
      ? { ...payload._requestHeaders }
      : { 'Content-Type': 'application/json' };

  const session = await createSpecialistSessionForHandoff(handoff.matchedAgent, requestBody);
  if (!session || !session.id) {
    throw new Error(`Failed to open specialist session for ${handoff.matchedAgent}`);
  }

  const handoffRequestBody = {
    ...requestBody,
    agent_name: handoff.matchedAgent
  };
  const handoffRouteContext =
    handoffRequestBody.route_context && typeof handoffRequestBody.route_context === 'object'
      ? { ...handoffRequestBody.route_context }
      : {};
  if (!handoffRouteContext.workspace_id && session.folder_id) {
    handoffRouteContext.workspace_id = session.folder_id;
  }
  handoffRequestBody.route_context = handoffRouteContext;

  const handoffHeaders = {
    ...requestHeaders,
    'Content-Type': 'application/json',
    'X-Session-ID': session.id
  };

  addMessageToChat(
    buildDisplayMessageForRequest(handoffRequestBody, fallbackMessage),
    true,
    false,
    false,
    isSlashCommand
  );

  if (chatStateMachine && chatStateMachine.isActive()) {
    chatStateMachine.think();
  }

  const response = await fetch('/api/chat', {
    method: 'POST',
    headers: handoffHeaders,
    body: JSON.stringify(handoffRequestBody),
    signal: chatStateMachine ? chatStateMachine.getSignal() : undefined
  });

  if (!response.ok) {
    throw new Error(`HTTP ${response.status}: ${response.statusText}`);
  }

  const data = await response.json();
  data._requestBody = { ...handoffRequestBody };
  data._requestHeaders = { ...handoffHeaders };
  updatePlannerIndicator(data?.planner_decision);

  if (chatStateMachine && chatStateMachine.isActive()) {
    chatStateMachine.process();
  }

  await handleChatResponsePayload(
    data,
    handoffRequestBody.question || fallbackMessage,
    isSlashCommand
  );
  return true;
}

function shouldRenderRouteBadge(routeMeta) {
  if (!routeMeta || !routeMeta.mode) return false;
  return (
    routeMeta.mode === 'utility_direct' ||
    routeMeta.mode === 'direct_tool' ||
    routeMeta.mode === 'specialist_handoff'
  );
}

function formatRouteBadgeText(routeMeta) {
  if (!routeMeta) return '';
  if (routeMeta.mode === 'utility_direct') {
    const toolPart = routeMeta.toolName ? `Tool ${routeMeta.toolName}` : 'Utility direct';
    const providerPart = routeMeta.provider ? ` · ${routeMeta.provider}` : '';
    return `${toolPart}${providerPart}`;
  }
  if (routeMeta.mode === 'direct_tool') {
    return routeMeta.toolName ? `Direct tool · ${routeMeta.toolName}` : 'Direct tool';
  }
  if (routeMeta.mode === 'specialist_handoff') {
    return 'Delegated to specialist workflow';
  }
  return routeMeta.mode;
}

function appendMessageToUI(message, isUser = false, isError = false, routeMeta = null) {
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

  const badgeMeta = shouldRenderRouteBadge(routeMeta) ? routeMeta : null;
  if (badgeMeta) {
    const badge = document.createElement('div');
    badge.className = `chat-route-badge mode-${badgeMeta.mode.replace(/[^a-z0-9_-]/g, '-')}`;
    badge.textContent = formatRouteBadgeText(badgeMeta);
    messageContent.appendChild(badge);
  }

  const messageBody = document.createElement('div');

  // Process message content (support markdown and JSON tables)
  if (!isUser) {
    // Try to detect and render JSON tables
    const tableContent = tryRenderJsonTable(message);
    if (tableContent) {
      messageBody.innerHTML = tableContent;
    } else if (typeof marked !== 'undefined' && typeof DOMPurify !== 'undefined') {
      messageBody.innerHTML = DOMPurify.sanitize(marked.parse(message));
    } else {
      messageBody.textContent = message;
    }
  } else {
    messageBody.textContent = message;
  }

  messageContent.appendChild(messageBody);
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
function addMessageToChat(
  message,
  isUser = false,
  isError = false,
  isSystemNotification = false,
  skipHistory = false,
  routeMeta = null
) {
  // Add to UI
  appendMessageToUI(message, isUser, isError, routeMeta);

  // Skip storing system notifications or intentionally non-persisted messages (e.g., slash commands)
  // Only store actual user queries and assistant responses that should be remembered
  if (isSystemNotification || skipHistory) {
    return;
  }

  // Store message in memory
  chatMessages.push({
    content: message,
    isUser: isUser,
    timestamp: new Date().toISOString(),
    routeMeta: routeMeta || undefined
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
      const result =
        typeof tc.result === 'string' ? tc.result : JSON.stringify(tc.result ?? '', null, 2);
      return [
        '<details>',
        `<summary>Tool: <code>${functionName}</code></summary>`,
        '<div style="margin-top:8px">',
        '<div><strong>Args</strong></div>',
        `<pre style="white-space:pre-wrap; margin:8px 0;">${escapeHtml(args)}</pre>`,
        '<div><strong>Result</strong></div>',
        `<pre style="white-space:pre-wrap; margin:8px 0;">${escapeHtml(result)}</pre>`,
        '</div>',
        '</details>'
      ].join('\n');
    })
    .join('\n\n');
}

function formatActionReceiptsForChat(receipts) {
  if (!Array.isArray(receipts) || receipts.length === 0) return '';

  return receipts
    .map((receipt, idx) => {
      const action = receipt.action || `Action ${idx + 1}`;
      const tool = receipt.tool_name ? `Tool: ${receipt.tool_name}` : '';
      const reason = receipt.reason || '';
      const status = receipt.success === false ? 'Failed' : 'Success';
      const duration =
        typeof receipt.duration_ms === 'number' ? `Duration: ${receipt.duration_ms}ms` : '';
      const targets =
        Array.isArray(receipt.targets) && receipt.targets.length > 0
          ? `Targets: ${receipt.targets.join(', ')}`
          : '';
      const locations =
        Array.isArray(receipt.locations) && receipt.locations.length > 0
          ? `Locations: ${receipt.locations.join(', ')}`
          : '';
      const preview = receipt.result_preview || '';

      const summaryParts = [action, status];
      if (tool) summaryParts.push(tool);

      return [
        '<details>',
        `<summary>${escapeHtml(summaryParts.join(' • '))}</summary>`,
        '<div style="margin-top:8px">',
        reason ? `<div><strong>Why:</strong> ${escapeHtml(reason)}</div>` : '',
        duration ? `<div><strong>${escapeHtml(duration)}</strong></div>` : '',
        targets ? `<div><strong>${escapeHtml(targets)}</strong></div>` : '',
        locations ? `<div><strong>${escapeHtml(locations)}</strong></div>` : '',
        preview
          ? `<div><strong>What changed</strong><pre style="white-space:pre-wrap; margin:8px 0;">${escapeHtml(preview)}</pre></div>`
          : '',
        '</div>',
        '</details>'
      ]
        .filter(Boolean)
        .join('\n');
    })
    .join('\n\n');
}

function formatActionPlanForChat(data) {
  const plan = data?.action_plan;
  const fallback =
    typeof data?.response === 'string' ? data.response : 'Action plan is ready for approval.';
  if (!plan || !Array.isArray(plan.steps) || plan.steps.length === 0) {
    return fallback;
  }

  const lines = plan.steps.map((step, idx) => {
    const title = step?.title ? String(step.title) : `Step ${idx + 1}`;
    const details = step?.details ? `\n   ${String(step.details)}` : '';
    return `${idx + 1}. ${title}${details}`;
  });

  return `**Planned Next Actions**\n${lines.join('\n')}\n\n${plan.summary || 'Approve to execute, or edit/cancel.'}`;
}

function renderActionPlanControls(planId, originalMessage) {
  if (!planId) return;
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const wrapper = document.createElement('div');
  wrapper.className = 'message-container mb-3 assistant-message';
  wrapper.dataset.planId = planId;

  const content = document.createElement('div');
  content.className = 'modern-card p-3 me-auto';
  content.style.maxWidth = '85%';
  content.style.background = 'var(--bg-secondary)';

  const title = document.createElement('div');
  title.style.fontSize = '12px';
  title.style.color = 'var(--text-muted)';
  title.style.marginBottom = '8px';
  title.textContent = 'Approval required';
  content.appendChild(title);

  const controls = document.createElement('div');
  controls.style.display = 'flex';
  controls.style.gap = '8px';
  controls.style.flexWrap = 'wrap';

  const approveBtn = document.createElement('button');
  approveBtn.type = 'button';
  approveBtn.className = 'btn btn-sm btn-success';
  approveBtn.textContent = 'Approve';

  const editBtn = document.createElement('button');
  editBtn.type = 'button';
  editBtn.className = 'btn btn-sm btn-outline-primary';
  editBtn.textContent = 'Edit';

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'btn btn-sm btn-outline-secondary';
  cancelBtn.textContent = 'Cancel';

  controls.appendChild(approveBtn);
  controls.appendChild(editBtn);
  controls.appendChild(cancelBtn);
  content.appendChild(controls);
  wrapper.appendChild(content);
  chatArea.appendChild(wrapper);
  if (window.scrollChatToBottomIfNeeded) {
    window.scrollChatToBottomIfNeeded();
  }

  const setDisabled = disabled => {
    approveBtn.disabled = disabled;
    editBtn.disabled = disabled;
    cancelBtn.disabled = disabled;
  };

  approveBtn.addEventListener('click', async () => {
    setDisabled(true);
    await executeApprovedActionPlan(planId);
  });

  editBtn.addEventListener('click', () => {
    const input = document.getElementById('input');
    if (input) {
      input.value = originalMessage || '';
      input.focus();
      input.style.height = 'auto';
      input.style.height = input.scrollHeight + 'px';
    }
    pendingActionPlanContexts.delete(planId);
    wrapper.remove();
  });

  cancelBtn.addEventListener('click', () => {
    pendingActionPlanContexts.delete(planId);
    wrapper.remove();
    addMessageToChat('Cancelled action plan.', false, false, true, true);
  });
}

function renderSpecialistCreationControls(payload, originalMessage, isSlashCommand) {
  const handoff = normalizeSpecialistHandoff(payload);
  if (!handoff || !handoff.requiresCreation) return;

  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const wrapper = document.createElement('div');
  wrapper.className = 'message-container mb-3 assistant-message';

  const content = document.createElement('div');
  content.className = 'modern-card p-3 me-auto';
  content.style.maxWidth = '85%';
  content.style.background = 'var(--bg-secondary)';

  const title = document.createElement('div');
  title.style.fontSize = '12px';
  title.style.color = 'var(--text-muted)';
  title.style.marginBottom = '8px';
  title.textContent = 'Specialist creation required';
  content.appendChild(title);

  const summary = document.createElement('div');
  summary.style.marginBottom = '12px';
  const suggestedName = handoff.suggestedAgentName || 'Specialist Agent';
  const intentLabel = handoff.intentLabel || 'this request';
  summary.textContent = `Create "${suggestedName}" to handle ${intentLabel}?`;
  content.appendChild(summary);

  const controls = document.createElement('div');
  controls.style.display = 'flex';
  controls.style.gap = '8px';
  controls.style.flexWrap = 'wrap';

  const createBtn = document.createElement('button');
  createBtn.type = 'button';
  createBtn.className = 'btn btn-sm btn-primary';
  createBtn.textContent = `Create ${suggestedName}`;

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'btn btn-sm btn-outline-secondary';
  cancelBtn.textContent = 'Not now';

  const openAgentsBtn = document.createElement('button');
  openAgentsBtn.type = 'button';
  openAgentsBtn.className = 'btn btn-sm btn-outline-primary';
  openAgentsBtn.textContent = 'Open Agents';

  controls.appendChild(createBtn);
  controls.appendChild(cancelBtn);
  controls.appendChild(openAgentsBtn);
  content.appendChild(controls);
  wrapper.appendChild(content);
  chatArea.appendChild(wrapper);

  if (window.scrollChatToBottomIfNeeded) {
    window.scrollChatToBottomIfNeeded();
  }

  const setDisabled = disabled => {
    createBtn.disabled = disabled;
    cancelBtn.disabled = disabled;
    openAgentsBtn.disabled = disabled;
  };

  createBtn.addEventListener('click', async () => {
    const originalHtml = createBtn.innerHTML;
    setDisabled(true);
    createBtn.innerHTML =
      '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Creating...';

    try {
      const createdAgentName = await createSpecialistAgentFromHandoff(payload, originalMessage);
      addMessageToChat(
        `Created "${createdAgentName}". Continuing in specialist chat.`,
        false,
        false,
        true,
        true
      );

      const resumedPayload = {
        ...payload,
        matched_agent: createdAgentName,
        requires_creation: false,
        specialist_handoff: {
          ...(payload && typeof payload.specialist_handoff === 'object'
            ? payload.specialist_handoff
            : {}),
          matched_agent: createdAgentName,
          requires_creation: false
        }
      };

      wrapper.remove();
      await executeSpecialistHandoff(resumedPayload, originalMessage, isSlashCommand);
    } catch (error) {
      appLog.error('Specialist creation failed:', error);
      addMessageToChat(`Error: ${error.message}`, false, true, false, isSlashCommand);
      createBtn.innerHTML = originalHtml;
      setDisabled(false);
    }
  });

  cancelBtn.addEventListener('click', () => {
    addMessageToChat('Specialist creation canceled.', false, false, true, true);
    wrapper.remove();
  });

  openAgentsBtn.addEventListener('click', () => {
    window.location.href = '/agents';
  });
}

let dependencyResolutionModalState = null;

function normalizeDependencyResolution(payload) {
  const raw = payload?.dependency_resolution || inferDependencyResolutionFromResponse(payload);
  if (!raw || typeof raw !== 'object') return null;

  const steps = Array.isArray(raw.steps)
    ? raw.steps.filter(step => step && typeof step === 'object')
    : [];

  return {
    title: String(raw.title || 'Action required').trim() || 'Action required',
    summary: String(raw.summary || '').trim(),
    recommendedSurface: String(raw.recommended_surface || '').trim(),
    retrySupported: Boolean(raw.retry_context?.supported),
    steps
  };
}

function inferDependencyResolutionFromResponse(payload) {
  const responseText = typeof payload?.response === 'string' ? payload.response.trim() : '';
  if (!responseText) return null;

  const lower = responseText.toLowerCase();
  if (!looksLikePermissionDeniedDependencyText(lower)) {
    return null;
  }

  const target = inferDependencyResolutionTarget(responseText);
  const title = target.displayName
    ? `${target.displayName} permission required`
    : 'Tool permission required';
  const summary = target.displayName
    ? `The current external agent blocked ${target.displayName} under its permission mode. Review agent permissions, then retry.`
    : 'The current external agent blocked a required tool under its permission mode. Review agent permissions, then retry.';

  const actions = [
    {
      type: 'open_url',
      label: 'Open Agents',
      description:
        'Review external agent permissions and allow the required tool or MCP, then retry this request.',
      variant: 'primary',
      url: '/agents'
    }
  ];

  if (target.serverName) {
    actions.push({
      type: 'open_url',
      label: 'Open MCP Settings',
      description: 'Confirm the required MCP connector is installed and available.',
      variant: 'secondary',
      url: '/mcp',
      server_name: target.serverName
    });
  }

  return {
    version: 1,
    title,
    summary,
    reason_code: 'provider_permission_denied',
    recommended_surface: 'modal',
    retry_context: {
      supported: false,
      strategy: 'repeat_request'
    },
    steps: [
      {
        id: 'external-tool-permission',
        type: 'tool_permission',
        display_name: target.displayName || 'Required tool',
        summary,
        risk_level: 'medium',
        actions
      }
    ]
  };
}

function looksLikePermissionDeniedDependencyText(lower) {
  return (
    (lower.includes('denied permission') ||
      lower.includes('permission mode') ||
      lower.includes('permission settings') ||
      lower.includes('permissions settings') ||
      lower.includes("isn't enabled in the current permission mode") ||
      lower.includes('is not enabled in the current permission mode') ||
      lower.includes('enable the mcp__') ||
      lower.includes('enable the `mcp__') ||
      lower.includes("enable the 'mcp__") ||
      lower.includes('enable the "mcp__')) &&
    (lower.includes('tool') || lower.includes('mcp') || lower.includes('reaper'))
  );
}

function inferDependencyResolutionTarget(responseText) {
  const lower = responseText.toLowerCase();
  if (
    lower.includes('mcp__ori-reaper') ||
    lower.includes('ori-reaper') ||
    lower.includes('reaper')
  ) {
    return { displayName: 'REAPER MCP tool', serverName: 'ori-reaper' };
  }

  const match = lower.match(/mcp__([a-z0-9._-]+)/);
  if (match && match[1]) {
    const serverName = String(match[1]).trim();
    const displayName = humanizeDependencyResolutionServerName(serverName);
    return {
      displayName: displayName ? `${displayName} MCP tool` : 'MCP tool',
      serverName
    };
  }

  if (lower.includes('mcp tool')) {
    return { displayName: 'MCP tool', serverName: '' };
  }

  return { displayName: 'Required tool', serverName: '' };
}

function humanizeDependencyResolutionServerName(serverName) {
  const cleaned = String(serverName || '')
    .replace(/^mcp__/, '')
    .replace(/^ori-/, '')
    .trim();
  if (!cleaned) return '';
  if (cleaned.toLowerCase() === 'reaper') return 'REAPER';
  return cleaned
    .split(/[-_.]+/)
    .filter(Boolean)
    .map(part =>
      part.length <= 3 ? part.toUpperCase() : `${part.charAt(0).toUpperCase()}${part.slice(1)}`
    )
    .join(' ');
}

function ensureDependencyResolutionModal() {
  if (dependencyResolutionModalState?.modalEl) {
    return dependencyResolutionModalState;
  }

  const modalEl = document.createElement('div');
  modalEl.className = 'modal fade';
  modalEl.id = 'dependencyResolutionModal';
  modalEl.tabIndex = -1;
  modalEl.setAttribute('aria-hidden', 'true');

  modalEl.innerHTML = `
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
      <div class="modal-content" style="background: var(--bg-primary); border: 1px solid var(--border-color);">
        <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
          <div>
            <div style="font-size: 12px; color: var(--text-muted); letter-spacing: 0.06em; text-transform: uppercase;">Dependency resolution</div>
            <h5 class="modal-title" id="dependencyResolutionTitle" style="color: var(--text-primary); margin-top: 4px;"></h5>
          </div>
          <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close" style="filter: invert(1);"></button>
        </div>
        <div class="modal-body">
          <p id="dependencyResolutionSummary" style="color: var(--text-secondary); margin-bottom: 1rem;"></p>
          <div id="dependencyResolutionSteps" class="d-flex flex-column gap-3"></div>
        </div>
      </div>
    </div>
  `;

  document.body.appendChild(modalEl);

  dependencyResolutionModalState = {
    modalEl,
    titleEl: modalEl.querySelector('#dependencyResolutionTitle'),
    summaryEl: modalEl.querySelector('#dependencyResolutionSummary'),
    stepsEl: modalEl.querySelector('#dependencyResolutionSteps')
  };

  return dependencyResolutionModalState;
}

function hideDependencyResolutionModal() {
  if (!dependencyResolutionModalState?.modalEl || !window.bootstrap) return;
  const modal =
    typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(dependencyResolutionModalState.modalEl)
      : bootstrap.Modal.getInstance(dependencyResolutionModalState.modalEl);
  modal?.hide();
}

function createDependencyActionButton(action, context) {
  const button = document.createElement('button');
  button.type = 'button';
  const variant = String(action?.variant || 'secondary')
    .trim()
    .toLowerCase();
  button.className = `btn btn-sm ${variant === 'primary' ? 'btn-primary' : variant === 'danger' ? 'btn-danger' : 'btn-outline-secondary'}`;
  button.textContent = String(action?.label || 'Continue').trim() || 'Continue';
  button.addEventListener('click', async () => {
    const originalLabel = button.innerHTML;
    button.disabled = true;
    button.innerHTML =
      '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Working...';
    try {
      await handleDependencyResolutionAction(action, context);
    } catch (error) {
      appLog.error('Dependency resolution action failed:', error);
      if (window.Toast) {
        Toast.error(error.message || 'Failed to resolve dependency');
      }
      button.innerHTML = originalLabel;
      button.disabled = false;
    }
  });
  return button;
}

function renderDependencyResolutionModal(
  resolution,
  payload,
  originalMessage,
  isSlashCommand,
  options = {}
) {
  const state = ensureDependencyResolutionModal();
  if (!state || !window.bootstrap) return false;
  const context = {
    payload,
    originalMessage,
    isSlashCommand,
    retry: typeof options.retry === 'function' ? options.retry : null
  };

  state.titleEl.textContent = resolution.title;
  state.summaryEl.textContent =
    resolution.summary || 'This request needs setup before it can continue.';
  state.stepsEl.innerHTML = '';

  resolution.steps.forEach(step => {
    const card = document.createElement('section');
    card.style.border = '1px solid var(--border-color)';
    card.style.borderRadius = '14px';
    card.style.padding = '0.9rem';
    card.style.background = 'var(--bg-secondary)';

    const heading = document.createElement('div');
    heading.style.display = 'flex';
    heading.style.justifyContent = 'space-between';
    heading.style.alignItems = 'center';
    heading.style.gap = '12px';

    const titleWrap = document.createElement('div');
    const title = document.createElement('div');
    title.style.color = 'var(--text-primary)';
    title.style.fontWeight = '600';
    title.textContent = String(step.display_name || step.type || 'Dependency').trim();
    titleWrap.appendChild(title);

    const summary = document.createElement('div');
    summary.style.color = 'var(--text-secondary)';
    summary.style.fontSize = '0.92rem';
    summary.style.marginTop = '4px';
    summary.textContent = String(step.summary || '').trim();
    titleWrap.appendChild(summary);

    heading.appendChild(titleWrap);

    if (step.risk_level) {
      const risk = document.createElement('span');
      risk.className = 'badge';
      risk.style.background =
        step.risk_level === 'high' ? 'rgba(220, 53, 69, 0.18)' : 'rgba(13, 110, 253, 0.14)';
      risk.style.color = step.risk_level === 'high' ? '#ffb3bd' : '#9ec5fe';
      risk.textContent = `${String(step.risk_level).trim()} risk`;
      heading.appendChild(risk);
    }

    card.appendChild(heading);

    const actionsWrap = document.createElement('div');
    actionsWrap.style.display = 'flex';
    actionsWrap.style.flexWrap = 'wrap';
    actionsWrap.style.gap = '8px';
    actionsWrap.style.marginTop = '12px';

    const actions = Array.isArray(step.actions) ? step.actions : [];
    actions.forEach(action => {
      actionsWrap.appendChild(createDependencyActionButton(action, context));
    });

    card.appendChild(actionsWrap);
    state.stepsEl.appendChild(card);
  });

  const modal =
    typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(state.modalEl)
      : bootstrap.Modal.getInstance(state.modalEl) || new bootstrap.Modal(state.modalEl);
  modal.show();
  return true;
}

async function retryDependencyResolutionRequest(payload, originalMessage, isSlashCommand) {
  const requestBody = payload?._requestBody
    ? { ...payload._requestBody }
    : { question: originalMessage };
  const headers = payload?._requestHeaders
    ? { ...payload._requestHeaders }
    : { 'Content-Type': 'application/json' };

  if (chatStateMachine) {
    chatStateMachine.send();
  }
  updateSendButton();

  try {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers,
      body: JSON.stringify(requestBody),
      signal: chatStateMachine ? chatStateMachine.getSignal() : undefined
    });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    data._requestBody = { ...requestBody };
    data._requestHeaders = { ...headers };
    updatePlannerIndicator(data?.planner_decision);
    await handleChatResponsePayload(data, originalMessage, isSlashCommand);
  } finally {
    if (chatStateMachine) {
      chatStateMachine.complete();
    }
    updateSendButton();
  }
}

async function handleDependencyResolutionAction(action, context) {
  const actionType = String(action?.type || '').trim();
  if (!actionType) {
    throw new Error('Missing dependency action type');
  }

  if (actionType === 'open_url') {
    const url = String(action?.url || '').trim();
    if (!url) throw new Error('Missing URL for dependency action');
    hideDependencyResolutionModal();
    window.open(url, '_blank', 'noopener');
    if (window.Toast) {
      Toast.info('Complete setup, then retry your request.');
    }
    return;
  }

  if (
    actionType === 'workspace_enable_mcp_binding' ||
    actionType === 'suppress_dependency_prompt'
  ) {
    const workspaceId = String(action?.workspace_id || '').trim();
    if (!workspaceId) {
      throw new Error('Dependency action requires a workspace');
    }

    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/dependency-actions`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: actionType,
          server_name: action?.server_name || '',
          skill_name: action?.skill_name || '',
          dependency_type: action?.dependency_type || '',
          preference_key: action?.preference_key || ''
        })
      }
    );
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to resolve dependency');
    }

    const result = await response.json();
    hideDependencyResolutionModal();
    if (window.Toast) {
      Toast.success(
        actionType === 'suppress_dependency_prompt'
          ? 'Prompt preference saved'
          : 'Dependency resolved'
      );
    }

    if (action?.auto_retry && result?.retry_ready) {
      if (typeof context?.retry === 'function') {
        await context.retry(action, result, context);
      } else {
        await retryDependencyResolutionRequest(
          context?.payload,
          context?.originalMessage,
          context?.isSlashCommand
        );
      }
    }
    return;
  }

  throw new Error(`Unsupported dependency action: ${actionType}`);
}

async function executeApprovedActionPlan(planId) {
  const context = pendingActionPlanContexts.get(planId);
  if (!context) {
    if (window.Toast) {
      Toast.warning('Action plan context expired. Please resubmit your request.');
    }
    return;
  }
  if (chatStateMachine && chatStateMachine.isActive()) {
    return;
  }

  if (chatStateMachine) {
    chatStateMachine.send();
  }
  updateSendButton();

  try {
    const requestBody = {
      ...context.requestBody,
      plan_before_action: true,
      approved_action_plan_id: planId
    };
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: context.headers,
      body: JSON.stringify(requestBody),
      signal: chatStateMachine ? chatStateMachine.getSignal() : undefined
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    data._requestBody = { ...requestBody };
    data._requestHeaders = { ...context.headers };
    updatePlannerIndicator(data?.planner_decision);
    await handleChatResponsePayload(data, context.originalMessage, context.isSlashCommand);
    pendingActionPlanContexts.delete(planId);
  } catch (error) {
    if (error.name === 'AbortError') {
      return;
    }
    addMessageToChat(`Error: ${error.message}`, false, true, false, context.isSlashCommand);
    if (window.Toast) {
      Toast.error('Failed to execute approved action plan');
    }
  } finally {
    if (chatStateMachine) {
      chatStateMachine.complete();
    }
    updateSendButton();
  }
}

async function handleChatResponsePayload(data, trimmedMessage, isSlashCommand) {
  const routeMeta = normalizeRouteMetadata(data);
  const specialistHandoff = normalizeSpecialistHandoff(data);
  const dependencyResolution = normalizeDependencyResolution(data);

  if (specialistHandoff && specialistHandoff.matchedAgent && !specialistHandoff.requiresCreation) {
    try {
      const handedOff = await executeSpecialistHandoff(data, trimmedMessage, isSlashCommand);
      if (handedOff) {
        return;
      }
    } catch (error) {
      appLog.error('Specialist handoff failed:', error);
      addMessageToChat(`Error: ${error.message}`, false, true, false, isSlashCommand);
      if (window.Toast) {
        Toast.error('Failed to continue in specialist chat');
      }
      return;
    }
  }

  if (data?.requires_approval && data?.approval_type === 'action_plan' && data?.action_plan_id) {
    const planMessage = formatActionPlanForChat(data);
    addMessageToChat(planMessage, false, false, true, true, routeMeta);
    pendingActionPlanContexts.set(data.action_plan_id, {
      requestBody: data._requestBody || { question: trimmedMessage },
      headers: data._requestHeaders || { 'Content-Type': 'application/json' },
      originalMessage: trimmedMessage,
      isSlashCommand
    });
    renderActionPlanControls(data.action_plan_id, trimmedMessage);
    if (window.sessionManager && window.sessionManager.onMessageSent) {
      window.sessionManager.onMessageSent();
    }
    EventBus.emit('chat:message:sent', { message: trimmedMessage, isSlashCommand });
    return;
  }

  const hasResponseField = Object.prototype.hasOwnProperty.call(data, 'response');
  const responseText = typeof data.response === 'string' ? data.response : null;
  const toolCallsText = formatToolCallsForChat(data.toolCalls);
  const receiptsText = formatActionReceiptsForChat(data.action_receipts);

  if (specialistHandoff && specialistHandoff.requiresCreation) {
    if (hasResponseField && responseText !== null && responseText.trim().length > 0) {
      addMessageToChat(responseText, false, false, false, isSlashCommand, routeMeta);
    } else {
      addMessageToChat(
        'This request needs a specialist before it can continue.',
        false,
        false,
        false,
        isSlashCommand,
        routeMeta
      );
    }

    if (receiptsText) {
      addMessageToChat(receiptsText, false, false, true, true, routeMeta);
    }

    renderSpecialistCreationControls(data, trimmedMessage, isSlashCommand);
    if (window.sessionManager && window.sessionManager.onMessageSent) {
      window.sessionManager.onMessageSent();
    }
    EventBus.emit('chat:message:sent', { message: trimmedMessage, isSlashCommand });
    return;
  }

  if (hasResponseField && responseText !== null) {
    if (responseText.trim().length > 0) {
      addMessageToChat(responseText, false, false, false, isSlashCommand, routeMeta);
    } else if (toolCallsText) {
      addMessageToChat(toolCallsText, false, false, false, isSlashCommand, routeMeta);
    } else {
      addMessageToChat('(no text response)', false, false, false, isSlashCommand, routeMeta);
    }

    if (receiptsText) {
      addMessageToChat(receiptsText, false, false, true, true, routeMeta);
    }

    if (window.sessionManager && window.sessionManager.onMessageSent) {
      window.sessionManager.onMessageSent();
    }

    EventBus.emit('chat:message:sent', { message: trimmedMessage, isSlashCommand });
    renderPlannerPlanSummary(data);
    await handleDynamicAgentApprovals(data);
    if (dependencyResolution) {
      renderDependencyResolutionModal(dependencyResolution, data, trimmedMessage, isSlashCommand);
    }
    return;
  }

  if (dependencyResolution) {
    addMessageToChat(
      dependencyResolution.summary || 'This request needs setup before it can continue.',
      false,
      false,
      false,
      isSlashCommand,
      routeMeta
    );
    renderDependencyResolutionModal(dependencyResolution, data, trimmedMessage, isSlashCommand);
    if (window.sessionManager && window.sessionManager.onMessageSent) {
      window.sessionManager.onMessageSent();
    }
    EventBus.emit('chat:message:sent', { message: trimmedMessage, isSlashCommand });
    return;
  }

  appLog.error('No response field found. Available fields:', Object.keys(data));
  const details = escapeHtml(JSON.stringify(data, null, 2));
  addMessageToChat(
    `Sorry, I received an unexpected response format.\n\n<details><summary>Raw response</summary><pre style="white-space:pre-wrap; margin:8px 0;">${details}</pre></details>`,
    false,
    true
  );
  if (window.Toast) {
    Toast.warning('Received unexpected response format');
  }
}

function updatePlannerIndicator(decision) {
  const indicator = document.getElementById('chatPlannerIndicator');
  const badge = document.getElementById('chatPlannerBadge');
  const text = document.getElementById('chatPlannerDecisionText');
  if (!indicator || !badge || !text) return;

  if (!decision) {
    indicator.style.display = 'none';
    return;
  }

  const mode = decision.mode || 'auto';
  const score =
    typeof decision.complexity_score === 'number' ? decision.complexity_score.toFixed(1) : '--';
  const threshold = typeof decision.threshold === 'number' ? decision.threshold.toFixed(1) : '--';
  const status = decision.multi_agent ? 'ON' : 'OFF';

  badge.textContent = `Multi-agent: ${mode}`;
  text.textContent = `Score ${score} vs ${threshold} => ${status}`;
  text.title = decision.rationale || '';
  indicator.style.display = 'block';
}

function renderPlannerPlanSummary(data) {
  if (!data || !data.orchestrated || !data.planner_plan) return;
  const tasks = data.planner_plan.tasks || [];
  if (!Array.isArray(tasks) || tasks.length === 0) return;

  const lines = tasks.map((task, idx) => `${idx + 1}. ${task.description || 'Task'}`);
  const message = `**Plan**\n${lines.join('\n')}`;
  addMessageToChat(message, false, false, true, true);
}

async function handleDynamicAgentApprovals(data) {
  if (
    !data ||
    !Array.isArray(data.dynamic_agent_requests) ||
    data.dynamic_agent_requests.length === 0
  ) {
    return;
  }
  const workspaceId = data.workspace_id;
  if (!workspaceId) return;

  const summary = data.dynamic_agent_requests
    .map(req => `- ${req.name} (${req.role || 'general'})`)
    .join('\n');

  const approve = window.confirm(`Dynamic agent creation requested:\n\n${summary}\n\nApprove?`);

  let resumeResult = null;
  for (const req of data.dynamic_agent_requests) {
    const response = await fetch('/api/orchestration/dynamic-agents/approve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: workspaceId,
        request_id: req.id,
        approve,
        approved_by: 'user'
      })
    });
    if (response.ok) {
      const respData = await response.json();
      if (respData.resume_result) {
        resumeResult = respData.resume_result;
      }
    }
  }

  if (approve) {
    addMessageToChat('✅ Approved dynamic agent creation.', false, false, true, true);
    if (resumeResult && resumeResult.final_output) {
      addMessageToChat(resumeResult.final_output, false, false, true, true);
    }
  } else {
    addMessageToChat('❌ Dynamic agent creation denied.', false, false, true, true);
  }
}

// Chat state machine reference (initialized in initializeApp)
let chatStateMachine = null;

// Handle /task chat command
async function handleTaskCommand(message) {
  const input = document.getElementById('input');
  if (input) {
    input.value = '';
    input.style.height = 'auto';
  }

  const sessionId = window.sessionManager?.getActiveSessionId?.();
  if (!sessionId) {
    addMessageToChat('/task', true, false, false, true);
    addMessageToChat('No active session. Please select or create a session first.', false, true);
    return;
  }

  // Parse command: /task or /task <description>
  const args = message.substring(5).trim(); // Remove "/task"

  if (!args) {
    // No args: display inline task list
    addMessageToChat('/task', true, false, false, true);
    await displayTaskList(sessionId);
    return;
  }

  // Create a new task
  addMessageToChat(`/task ${args}`, true, false, false, true);

  try {
    const task = await API.post(`/api/sessions/${sessionId}/tasks`, { description: args });

    addMessageToChat(`✓ Task created: "${task.description}"`, false, false);

    // Refresh session tasks
    if (window.sessionManager?.loadSessionTasks) {
      await window.sessionManager.loadSessionTasks();
    }
  } catch (error) {
    appLog.error('Failed to create task:', error);
    addMessageToChat(`✗ Failed to create task: ${error.message}`, false, true);
  }
}

// Display task list inline in chat
async function displayTaskList(sessionId) {
  try {
    const data = await API.get(`/api/sessions/${sessionId}/tasks`);
    const tasks = data.tasks || [];

    if (tasks.length === 0) {
      addMessageToChat(
        'No tasks for this session. Use `/task <description>` to create one.',
        false,
        false
      );
      return;
    }

    // Build task list message
    let message = `**Session Tasks** (${data.counts?.pending || 0} pending, ${data.counts?.completed || 0} completed)\n\n`;

    // Sort: pending first
    const sortedTasks = [...tasks].sort((a, b) => {
      const aCompleted = a.status === 'completed' ? 1 : 0;
      const bCompleted = b.status === 'completed' ? 1 : 0;
      return aCompleted - bCompleted;
    });

    sortedTasks.forEach(task => {
      const checkbox = task.status === 'completed' ? '☑' : '☐';
      const strikethrough = task.status === 'completed' ? '~~' : '';
      message += `${checkbox} ${strikethrough}${task.description}${strikethrough}\n`;
    });

    message += '\n_Use `/task <description>` to add a task_';

    addMessageToChat(message, false, false);
  } catch (error) {
    appLog.error('Failed to load tasks:', error);
    addMessageToChat(`✗ Failed to load tasks: ${error.message}`, false, true);
  }
}

function isTextLikeMimeType(mimeType) {
  if (!mimeType) return false;
  const lower = mimeType.toLowerCase();
  return (
    lower.startsWith('text/') ||
    lower.includes('json') ||
    lower.includes('xml') ||
    lower.includes('csv') ||
    lower.includes('markdown') ||
    lower.includes('html')
  );
}

function hasTextExtension(filename) {
  const ext = filename.split('.').pop()?.toLowerCase();
  return ['txt', 'md', 'json', 'xml', 'html', 'csv'].includes(ext);
}

function shouldTreatContentAsBase64(file) {
  if (file.encoding === 'base64') return true;
  if (file.encoding === 'text') return false;
  if (isTextLikeMimeType(file.type)) return false;
  if (file.name && hasTextExtension(file.name)) return false;
  return true;
}

function base64ToBlob(base64, mimeType) {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new Blob([bytes], { type: mimeType || 'application/octet-stream' });
}

function buildUploadBlob(file) {
  const mimeType = file.type || 'application/octet-stream';
  if (shouldTreatContentAsBase64(file)) {
    try {
      return base64ToBlob(file.content || '', mimeType);
    } catch (error) {
      appLog.warn('Failed to decode attachment, falling back to text upload', error);
    }
  }

  const textMime = isTextLikeMimeType(mimeType) ? mimeType : 'text/plain';
  return new Blob([file.content || ''], { type: textMime });
}

async function uploadFileToSession(sessionId, file) {
  const formData = new FormData();
  formData.append('file', buildUploadBlob(file), file.name || 'attachment');

  const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/files/upload`, {
    method: 'POST',
    body: formData
  });

  if (!response.ok) {
    let message = 'Failed to save attachment';
    try {
      const data = await response.json();
      if (data.message) message = data.message;
    } catch (error) {
      appLog.warn('Failed to parse upload error response', error);
    }
    throw new Error(message);
  }

  const data = await response.json();
  return data.file || null;
}

async function uploadFileToWorkspace(workspaceId, file, entry) {
  if (!workspaceId || !file) return;

  const filename = entry?.name || file.name || 'attachment';
  const formData = new FormData();
  formData.append('file', buildUploadBlob(file), filename);
  formData.append('title', filename);

  const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/files`, {
    method: 'POST',
    body: formData
  });

  if (!response.ok) {
    let message = 'Failed to add file to workspace';
    try {
      const data = await response.json();
      if (data.message) message = data.message;
    } catch (error) {
      appLog.warn('Failed to parse workspace attachment error response', error);
    }
    throw new Error(message);
  }
}

async function persistUploadedFilesToSession(sessionId, files, workspaceId) {
  if (!sessionId || !files || files.length === 0) return;

  try {
    const results = await Promise.allSettled(
      files.map(file => uploadFileToSession(sessionId, file))
    );
    const failures = results.filter(result => result.status === 'rejected');
    const successes = results
      .map((result, index) =>
        result.status === 'fulfilled' && result.value
          ? { file: files[index], entry: result.value }
          : null
      )
      .filter(Boolean);

    if (failures.length > 0 && window.Toast) {
      const label = failures.length === 1 ? 'attachment' : 'attachments';
      Toast.warning(`${failures.length} ${label} failed to save to session files`);
    }

    if (window.sessionFileManager && window.sessionFileManager.sessionId === sessionId) {
      window.sessionFileManager.loadFiles();
    }

    if (workspaceId && successes.length > 0) {
      const workspaceUploadResults = await Promise.allSettled(
        successes.map(item => uploadFileToWorkspace(workspaceId, item.file, item.entry))
      );
      const workspaceUploadFailures = workspaceUploadResults.filter(
        result => result.status === 'rejected'
      );
      if (workspaceUploadFailures.length > 0 && window.Toast) {
        Toast.warning('Some files could not be copied into the workspace');
      }
      if (window.EventBus) {
        EventBus.emit('workspace:files:updated', { workspaceId });
      }
    }
  } catch (error) {
    appLog.error('Failed to persist chat attachments to session files', error);
    if (window.Toast) {
      Toast.warning('Failed to save attachments to session files');
    }
  }
}

function extractWorkspaceIdFromPath(pathname) {
  const path = String(pathname || '').trim();
  const match = path.match(/^\/workspaces\/([^/]+)/i);
  if (!match || !match[1]) return '';
  try {
    return decodeURIComponent(match[1]);
  } catch (_error) {
    return match[1];
  }
}

function inferChatRouteSurface(pathname, workspaceId) {
  const path = String(pathname || '')
    .trim()
    .toLowerCase();
  if (!path) return workspaceId ? 'workspace_detail' : 'dashboard';
  if (path.startsWith('/workspaces/')) {
    if (path.includes('/canvas')) return 'workspace_canvas';
    return 'workspace_detail';
  }
  if (path.startsWith('/workspaces')) return 'workspace_hub';
  if (path.startsWith('/chat')) return workspaceId ? 'workspace_chat' : 'chat';
  if (path.startsWith('/dashboard') || path === '/') return 'dashboard';
  return workspaceId ? 'workspace_detail' : 'dashboard';
}

function normalizeRouteContextForChat(routeContext) {
  if (!routeContext || typeof routeContext !== 'object') return null;
  const normalized = {
    surface: String(routeContext.surface || '')
      .trim()
      .toLowerCase(),
    page_path: String(routeContext.page_path || '').trim(),
    workspace_id: String(routeContext.workspace_id || '').trim(),
    origin: String(routeContext.origin || '').trim()
  };
  if (!normalized.workspace_id && normalized.page_path) {
    normalized.workspace_id = extractWorkspaceIdFromPath(normalized.page_path);
  }
  return normalized;
}

function buildChatRequestRouteContext(overrideRouteContext) {
  const normalizedOverride = normalizeRouteContextForChat(overrideRouteContext);
  const pathname = String(window.location?.pathname || '/').trim() || '/';
  const activeWorkspaceId = String(
    window.sessionManager?.getActiveSession?.()?.folder_id || ''
  ).trim();
  const workspaceIdFromPath = extractWorkspaceIdFromPath(pathname);

  const routeContext = {
    page_path: (normalizedOverride && normalizedOverride.page_path) || pathname,
    workspace_id:
      (normalizedOverride && normalizedOverride.workspace_id) ||
      workspaceIdFromPath ||
      activeWorkspaceId,
    origin: (normalizedOverride && normalizedOverride.origin) || 'chat'
  };

  routeContext.surface =
    (normalizedOverride && normalizedOverride.surface) ||
    inferChatRouteSurface(routeContext.page_path, routeContext.workspace_id);

  return routeContext;
}

async function expandAttachedChatNotes(message, attachedNotes) {
  if (!Array.isArray(attachedNotes) || attachedNotes.length === 0) {
    return message;
  }

  const sections = [];
  for (const note of attachedNotes) {
    const noteId = String(note?.id || '').trim();
    const noteName = String(note?.name || 'Untitled Note').trim() || 'Untitled Note';
    let noteContent = '';

    if (noteId) {
      try {
        const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
        if (response.ok) {
          const payload = await response.json().catch(() => null);
          noteContent = String(payload?.content || '').trim();
        }
      } catch (error) {
        appLog.warn('Failed to fetch attached note content', {
          noteId,
          error: error && error.message ? error.message : error
        });
      }
    }

    if (!noteContent) {
      noteContent = String(note?.preview || '').trim();
    }
    if (!noteContent) {
      noteContent = '[Note content unavailable]';
    }

    sections.push(`---\n📝 **Attached Note: ${noteName}**\n\n${noteContent}\n---`);
  }

  if (sections.length === 0) {
    return message;
  }

  return `${message}\n\n${sections.join('\n\n')}`;
}

// Send message to chat API
async function sendMessage(message) {
  const routeContextOverride = pendingChatRouteContext;
  pendingChatRouteContext = null;

  // Check if state machine is active (replaces isWaitingForResponse)
  if (chatStateMachine && chatStateMachine.isActive()) return;

  const rawMessage = String(message || '').trim();
  if (!rawMessage) return;

  // Handle /task command
  if (rawMessage.startsWith('/task')) {
    await handleTaskCommand(rawMessage);
    return;
  }

  // Add to history
  promptHistory.unshift(rawMessage);
  historyIndex = -1;

  // Get uploaded files
  const uploadedFiles = window.getUploadedFiles ? window.getUploadedFiles() : [];
  const attachedNotes = window.getAttachedChatNotes ? window.getAttachedChatNotes() : [];
  const activeSessionId = window.sessionManager?.getActiveSessionId?.();
  const activeSession = window.sessionManager?.getActiveSession?.();
  const activeWorkspaceId = activeSession?.folder_id;
  const filesToPersist = uploadedFiles.slice();
  let requestMessage = rawMessage;

  // Expand @notename references if sessionManager is available
  if (window.sessionManager?.expandNoteReferences) {
    requestMessage = await window.sessionManager.expandNoteReferences(requestMessage);
  }
  if (attachedNotes.length > 0) {
    requestMessage = await expandAttachedChatNotes(requestMessage, attachedNotes);
  }

  if (activeSessionId && filesToPersist.length > 0) {
    persistUploadedFilesToSession(activeSessionId, filesToPersist, activeWorkspaceId);
  }

  // Add user message to chat (including file info if any)
  let displayMessage = rawMessage;
  if (attachedNotes.length > 0) {
    const noteNames = attachedNotes.map(note => note.name || 'Untitled Note').join(', ');
    displayMessage += `\n\n📝 Notes: ${noteNames}`;
  }
  if (uploadedFiles.length > 0) {
    const fileNames = uploadedFiles.map(f => f.name).join(', ');
    displayMessage += `\n\n📎 Attached: ${fileNames}`;
  }
  const isSlashCommand = rawMessage.startsWith('/');
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
    const routeContext = buildChatRequestRouteContext(routeContextOverride);

    // Prepare request body with files
    const requestBody = {
      question: requestMessage,
      route_context: routeContext
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
    const multiAgentModeSelect = document.getElementById('multiAgentMode');
    if (multiAgentModeSelect && multiAgentModeSelect.value) {
      requestBody.multi_agent_mode = multiAgentModeSelect.value;
    }
    const planBeforeActionToggle = document.getElementById('planBeforeActionToggle');
    if (planBeforeActionToggle?.checked) {
      requestBody.plan_before_action = true;
    }

    // Build headers with session ID for multi-tab support
    const chatHeaders = {
      'Content-Type': 'application/json'
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

    appLog.debug('Received chat response:', data);
    updatePlannerIndicator(data?.planner_decision);

    data._requestBody = { ...requestBody };
    data._requestHeaders = { ...chatHeaders };

    // Clear uploaded files after successful send
    if (window.clearFilesAfterSend) {
      window.clearFilesAfterSend();
    }
    if (window.clearAttachedNotesAfterSend) {
      window.clearAttachedNotesAfterSend();
    }

    // Transition to processing state while formatting response
    // Check isActive() to avoid race condition if user cancelled
    if (chatStateMachine && chatStateMachine.isActive()) {
      chatStateMachine.process();
    }
    await handleChatResponsePayload(data, rawMessage, isSlashCommand);
  } catch (error) {
    // Handle user cancellation gracefully
    if (error.name === 'AbortError') {
      appLog.debug('Request cancelled by user');
      return;
    }

    appLog.error('Chat error:', error);
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

// Send a message to chat programmatically (used by task execution, etc.)
// Exposed globally so other modules can trigger chat messages
window.sendMessageToChat = function (message, options) {
  const routeContext = options && typeof options === 'object' ? options.routeContext : null;
  pendingChatRouteContext = routeContext || null;
  const input = document.getElementById('input');
  if (input) {
    input.value = message;
  }
  return sendMessage(message);
};

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
  const planBeforeActionToggle = document.getElementById('planBeforeActionToggle');

  if (!input || !sendBtn) {
    appLog.warn('Chat elements not found');
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
  input.addEventListener('keydown', e => {
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

  if (planBeforeActionToggle) {
    const savedPlanBeforeAction = localStorage.getItem(PLAN_BEFORE_ACTION_STORAGE_KEY);
    if (savedPlanBeforeAction !== null) {
      planBeforeActionToggle.checked = savedPlanBeforeAction === 'true';
    }
    planBeforeActionToggle.addEventListener('change', () => {
      localStorage.setItem(
        PLAN_BEFORE_ACTION_STORAGE_KEY,
        planBeforeActionToggle.checked ? 'true' : 'false'
      );
    });
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

  setupChatWebSearchToggle();

  appLog.debug('Chat functionality initialized');
}

// ---- Chat Panel (Drawer) ----
function setupChatPanel() {
  const panel = document.getElementById('chatPanel');
  if (!panel) return;

  const closeBtn = document.getElementById('chatPanelClose');
  const backdrop = document.getElementById('chatPanelBackdrop');
  const resizeHandle = document.getElementById('chatPanelResize');
  const input = document.getElementById('input');

  // Restore saved width
  const savedWidth = localStorage.getItem('chatPanelWidth');
  if (savedWidth) {
    panel.style.width = savedWidth;
  }

  const open = () => {
    document.body.classList.add('chat-panel-open');
    panel.setAttribute('aria-hidden', 'false');
    if (backdrop) backdrop.setAttribute('aria-hidden', 'false');
    setTimeout(() => input?.focus(), 50);
  };

  const close = () => {
    document.body.classList.remove('chat-panel-open');
    panel.setAttribute('aria-hidden', 'true');
    if (backdrop) backdrop.setAttribute('aria-hidden', 'true');
  };

  const toggle = () => {
    if (document.body.classList.contains('chat-panel-open')) {
      close();
    } else {
      open();
    }
  };

  closeBtn?.addEventListener('click', close);
  backdrop?.addEventListener('click', close);

  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && document.body.classList.contains('chat-panel-open')) {
      close();
    }
  });

  // Resize functionality
  if (resizeHandle) {
    let isResizing = false;
    let startX = 0;
    let startWidth = 0;

    const startResize = e => {
      isResizing = true;
      startX = e.clientX;
      startWidth = panel.offsetWidth;
      resizeHandle.classList.add('resizing');
      document.body.style.cursor = 'ew-resize';
      document.body.style.userSelect = 'none';
      e.preventDefault();
    };

    const doResize = e => {
      if (!isResizing) return;
      const diff = startX - e.clientX;
      const newWidth = Math.min(Math.max(startWidth + diff, 360), window.innerWidth * 0.8);
      panel.style.width = `${newWidth}px`;
    };

    const stopResize = () => {
      if (!isResizing) return;
      isResizing = false;
      resizeHandle.classList.remove('resizing');
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      // Save width preference
      localStorage.setItem('chatPanelWidth', panel.style.width);
    };

    resizeHandle.addEventListener('mousedown', startResize);
    document.addEventListener('mousemove', doResize);
    document.addEventListener('mouseup', stopResize);
  }

  window.chatPanel = {
    open,
    close,
    toggle,
    isOpen: () => document.body.classList.contains('chat-panel-open')
  };
}

// ---- Sidebar Functionality ----
// Sidebar functionality has been moved to modular files:
// - js/modules/agents.js - Agent management
// - js/modules/skills.js - Skills management
// - js/modules/settings.js - Settings management
// - js/modules/sidebar.js - Main sidebar controller

// Export functions for use by session manager
window.appendMessageToUI = appendMessageToUI;
window.clearChatHistory = clearChatHistory;
window.replaceChatHistoryMessages = replaceChatHistoryMessages;

function setupSkillsDropdown() {
  const btn = document.getElementById('skillsDropdownBtn');
  const dropdown = document.getElementById('skillsDropdown');
  const content = document.getElementById('skillsDropdownContent');
  if (!btn || !dropdown || !content) return;

  let skillsCache = null;
  let cacheTime = 0;
  const CACHE_TTL = 30000;

  async function fetchSkills() {
    const now = Date.now();
    if (skillsCache && now - cacheTime < CACHE_TTL) {
      return skillsCache;
    }
    try {
      const res = await fetch('/api/skills');
      if (!res.ok) throw new Error('Failed to fetch skills');
      const data = await res.json();
      skillsCache = data.skills || [];
      cacheTime = now;
      return skillsCache;
    } catch (err) {
      appLog.error('Skills fetch error:', err);
      return null;
    }
  }

  function renderSkills(skills) {
    if (!skills) {
      content.innerHTML =
        '<div style="padding: 12px; text-align: center; color: var(--text-muted); font-size: 12px;">Failed to load skills</div>';
      return;
    }
    const runnableSkills = skills.filter(skill => {
      const enabled = skill?.enabled !== false;
      const valid =
        !Array.isArray(skill?.validation_errors) || skill.validation_errors.length === 0;
      const trusted = !skill?.has_scripts || Boolean(skill?.trusted);
      return enabled && valid && trusted;
    });
    if (runnableSkills.length === 0) {
      content.innerHTML =
        '<div style="padding: 12px; text-align: center; color: var(--text-muted); font-size: 12px;">No skills available</div>';
      return;
    }
    content.innerHTML = runnableSkills
      .map(skill => {
        const desc = skill.description || 'Run /' + skill.name;
        const src = skill.source || 'local';
        return `<button class="skill-item" data-skill="${skill.name}" style="display: block; width: 100%; text-align: left; padding: 8px 12px; border: none; background: none; cursor: pointer; font-size: 12px; color: var(--text-primary); transition: background 0.15s;">
        <div style="font-weight: 500;">${skill.name} <span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--bg-secondary); color: var(--text-muted);">${src}</span></div>
        <div style="font-size: 11px; color: var(--text-muted); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">${desc}</div>
      </button>`;
      })
      .join('');

    content.querySelectorAll('.skill-item').forEach(item => {
      item.addEventListener('mouseenter', () => (item.style.background = 'var(--bg-secondary)'));
      item.addEventListener('mouseleave', () => (item.style.background = 'none'));
      item.addEventListener('click', () => {
        const name = item.dataset.skill;
        dropdown.style.display = 'none';
        if (window.sendMessageToChat) {
          window.sendMessageToChat('/' + name);
        }
      });
    });
  }

  btn.addEventListener('click', async () => {
    const isVisible = dropdown.style.display !== 'none';
    if (isVisible) {
      dropdown.style.display = 'none';
      return;
    }
    dropdown.style.display = 'block';
    content.innerHTML =
      '<div style="padding: 12px; text-align: center; color: var(--text-muted); font-size: 12px;">Loading...</div>';
    const skills = await fetchSkills();
    renderSkills(skills);
  });

  document.addEventListener('click', e => {
    if (!btn.contains(e.target) && !dropdown.contains(e.target)) {
      dropdown.style.display = 'none';
    }
  });
}

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

    appLog.debug('Chat state machine initialized');
  } catch (error) {
    appLog.error('Failed to initialize chat state machine:', error);
    // Fall back to simple behavior without state machine
  }

  // Initialize chat auto-scroll
  try {
    const autoScrollModule = await import('./modules/chat-auto-scroll.js');
    autoScrollModule.initChatAutoScroll();

    // Clean up on page unload
    window.addEventListener('beforeunload', () => {
      autoScrollModule.cleanupChatAutoScroll();
    });

    appLog.debug('Chat auto-scroll initialized');
  } catch (error) {
    appLog.error('Failed to initialize auto-scroll:', error);
  }

  // Set up chat functionality
  setupChat();
  setupChatPanel();

  // Set up skills dropdown
  setupSkillsDropdown();

  // Set up agent display click handler (navigate to agent details)
  setupAgentDisplayClick();

  // Load Assistant/session display state and restore any local Assistant history.
  await refreshAgentDisplay();

  // Initialize onboarding for first-time users
  try {
    const { onboardingManager } = await import('./modules/onboarding.js');
    await onboardingManager.init();
  } catch (error) {
    appLog.error('Failed to initialize onboarding:', error);
    // Silent fail - onboarding is optional
  }

  // Sidebar functionality is now handled by modular files

  appLog.info('App initialized successfully');
  EventBus.emit('app:initialized');
}

function refreshSystemModelDisplayIfVisible() {
  if (document.hidden) return;
  refreshSystemModelDisplay();
}

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', function () {
  // Keep the navbar out of the heavier app bootstrap path. Task/detail pages
  // should resolve "Loading..." even if chat or dashboard initialization stalls.
  refreshSystemModelDisplay();
  initializeApp();
});

window.addEventListener('pageshow', refreshSystemModelDisplayIfVisible);
document.addEventListener('visibilitychange', refreshSystemModelDisplayIfVisible);

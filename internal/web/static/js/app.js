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

  // Process message content (support markdown)
  if (typeof marked !== 'undefined' && !isUser) {
    messageContent.innerHTML = marked.parse(message);
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

  // Add user message to chat
  addMessageToChat(trimmedMessage, true);
  
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
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        question: trimmedMessage
      })
    });

    hideTypingIndicator();

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    
    console.log('Received data:', data);
    console.log('data.response:', data.response);
    console.log('typeof data.response:', typeof data.response);
    
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



// Initialize application
function initializeApp() {
  // Set up dark mode functionality
  setupDarkMode();
  
  // Set up chat functionality
  setupChat();
  
  // Sidebar functionality is now handled by modular files
  
  console.log('App initialized successfully');
}

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', function() {
  initializeApp();
});

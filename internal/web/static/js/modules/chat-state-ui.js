// Chat State UI Module
// Renders the state indicator in the chat area with cancel support

import { ChatState, chatStateMachine } from './chat-state.js';

// Configuration for each state
const stateConfig = {
  [ChatState.IDLE]: null,
  [ChatState.SENDING]: {
    text: 'Sending',
    showCancel: true,
    showElapsed: false
  },
  [ChatState.THINKING]: {
    text: 'Thinking',
    showCancel: true,
    showElapsed: true
  },
  [ChatState.PROCESSING]: {
    text: 'Processing',
    showCancel: false,
    showElapsed: false
  }
};

let indicatorElement = null;
let unsubscribe = null;

/**
 * Initialize the chat state UI
 * Subscribes to state machine changes and renders indicator
 */
export function initChatStateUI() {
  // Clean up any existing state to prevent duplicates on hot reload
  cleanupChatStateUI();

  unsubscribe = chatStateMachine.subscribe(handleStateChange);
}

/**
 * Handle state changes from the state machine
 * @param {object} param0 - State change data
 */
function handleStateChange({ oldState, newState, type, elapsed }) {
  // Handle tick events (elapsed time updates)
  if (type === 'tick') {
    updateElapsedTime(elapsed);
    return;
  }

  const config = stateConfig[newState];

  if (!config) {
    removeIndicator();
    return;
  }

  showIndicator(config, newState);
}

/**
 * Show the state indicator with given configuration
 * @param {object} config - State configuration
 * @param {string} state - Current state name
 */
function showIndicator(config, state) {
  const sendBtn = document.getElementById('sendBtn');
  if (!sendBtn) return;

  // Create indicator if it doesn't exist
  if (!indicatorElement) {
    indicatorElement = createIndicatorElement();
  }

  // Insert indicator before the send button if not already there
  if (!sendBtn.parentNode.contains(indicatorElement)) {
    sendBtn.parentNode.insertBefore(indicatorElement, sendBtn);
  }

  // Update content
  const textEl = indicatorElement.querySelector('.state-text');
  const elapsedEl = indicatorElement.querySelector('.state-elapsed');
  const cancelBtn = indicatorElement.querySelector('.state-cancel');

  if (textEl) textEl.textContent = config.text;

  if (elapsedEl) {
    elapsedEl.style.display = config.showElapsed ? 'inline' : 'none';
    elapsedEl.textContent = '0s';
  }

  if (cancelBtn) {
    cancelBtn.style.display = config.showCancel ? 'inline-flex' : 'none';
  }

  // Update class for state-specific styling
  indicatorElement.className = `chat-state-indicator state-${state}`;
  indicatorElement.style.display = 'flex';
}

/**
 * Create the indicator DOM element
 * @returns {HTMLElement}
 */
function createIndicatorElement() {
  const div = document.createElement('div');
  div.id = 'chatStateIndicator';
  div.className = 'chat-state-indicator';
  // ARIA live region for screen reader announcements
  div.setAttribute('role', 'status');
  div.setAttribute('aria-live', 'polite');
  div.innerHTML = `
    <div class="state-content">
      <span class="state-spinner"></span>
      <span class="state-text"></span>
      <span class="state-elapsed"></span>
    </div>
    <button type="button" class="state-cancel" title="Cancel request" aria-label="Cancel request">
      <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/>
      </svg>
    </button>
  `;

  // Add cancel button handler
  const cancelBtn = div.querySelector('.state-cancel');
  if (cancelBtn) {
    cancelBtn.addEventListener('click', handleCancel);
  }

  return div;
}

/**
 * Handle cancel button click
 */
function handleCancel() {
  chatStateMachine.cancel();
  if (window.Toast) {
    Toast.info('Request cancelled');
  }
}

/**
 * Update the elapsed time display
 * @param {number} seconds - Elapsed seconds
 */
function updateElapsedTime(seconds) {
  if (!indicatorElement) return;

  const elapsedEl = indicatorElement.querySelector('.state-elapsed');
  if (elapsedEl && elapsedEl.style.display !== 'none') {
    elapsedEl.textContent = formatElapsedTime(seconds);
  }
}

/**
 * Format elapsed time for display
 * @param {number} seconds - Total seconds
 * @returns {string} Formatted time string
 */
function formatElapsedTime(seconds) {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${mins}m ${secs}s`;
}

/**
 * Remove/hide the indicator
 */
function removeIndicator() {
  if (indicatorElement) {
    indicatorElement.style.display = 'none';
  }
}

/**
 * Clean up the chat state UI
 * Removes the indicator element and unsubscribes from state changes
 */
export function cleanupChatStateUI() {
  if (unsubscribe) {
    unsubscribe();
    unsubscribe = null;
  }

  if (indicatorElement) {
    const cancelBtn = indicatorElement.querySelector('.state-cancel');
    if (cancelBtn) {
      cancelBtn.removeEventListener('click', handleCancel);
    }
    indicatorElement.remove();
    indicatorElement = null;
  }
}

/**
 * Get the current indicator element (for testing)
 * @returns {HTMLElement|null}
 */
export function getIndicatorElement() {
  return indicatorElement;
}

// Chat Auto-Scroll Module
// Smart scroll behavior with "Jump to bottom" button

/** @type {HTMLElement|null} */
let chatArea = null;
/** @type {HTMLButtonElement|null} */
let jumpButton = null;
/** @type {boolean} */
let isUserScrolledUp = false;
/** @type {number} Pixels from bottom to consider "at bottom" */
const scrollThreshold = 100;
/** @type {number|null} RAF handle for scroll throttling */
let scrollRAF = null;

/**
 * Initialize the auto-scroll module
 */
export function initChatAutoScroll() {
  // Clean up any existing state to prevent duplicates
  cleanupChatAutoScroll();

  chatArea = document.getElementById('chatArea');
  if (!chatArea) {
    console.warn('Chat area not found for auto-scroll');
    return;
  }

  // Create jump button
  createJumpButton();

  // Listen for scroll events (throttled with RAF)
  chatArea.addEventListener('scroll', handleScroll, { passive: true });

  // Initial state check
  checkScrollPosition();

  // Set up global references
  window.scrollChatToBottom = scrollToBottom;
  window.scrollChatToBottomIfNeeded = scrollToBottomIfNeeded;
}

/**
 * Handle scroll events (throttled with requestAnimationFrame)
 */
function handleScroll() {
  if (scrollRAF) return;
  scrollRAF = requestAnimationFrame(() => {
    checkScrollPosition();
    scrollRAF = null;
  });
}

/**
 * Check if user is scrolled up from bottom
 */
function checkScrollPosition() {
  if (!chatArea) return;

  const { scrollTop, scrollHeight, clientHeight } = chatArea;
  const distanceFromBottom = scrollHeight - scrollTop - clientHeight;

  isUserScrolledUp = distanceFromBottom > scrollThreshold;

  if (jumpButton) {
    jumpButton.style.display = isUserScrolledUp ? 'flex' : 'none';
  }
}

/**
 * Click handler wrapper for scroll button
 */
function handleJumpClick() {
  scrollToBottom(true);
}

/**
 * Create the "Jump to bottom" button
 */
function createJumpButton() {
  if (jumpButton) return;

  jumpButton = document.createElement('button');
  jumpButton.id = 'jumpToBottomBtn';
  jumpButton.className = 'jump-to-bottom-btn';
  jumpButton.type = 'button';
  jumpButton.setAttribute('aria-label', 'Jump to latest messages');
  jumpButton.innerHTML = `
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6 1.41-1.41z"/>
    </svg>
    <span>New messages</span>
  `;

  jumpButton.addEventListener('click', handleJumpClick);

  // Insert button near chat area
  if (chatArea.parentNode) {
    chatArea.parentNode.style.position = 'relative';
    chatArea.parentNode.appendChild(jumpButton);
  }

  // Initially hidden
  jumpButton.style.display = 'none';
}

/**
 * Scroll to the bottom of the chat area
 * @param {boolean} smooth - Use smooth scrolling animation
 */
export function scrollToBottom(smooth = true) {
  if (!chatArea) return;

  // Use requestAnimationFrame to ensure DOM is updated
  requestAnimationFrame(() => {
    chatArea.scrollTo({
      top: chatArea.scrollHeight,
      behavior: smooth ? 'smooth' : 'instant'
    });

    // Reset scrolled up state
    isUserScrolledUp = false;
    if (jumpButton) {
      jumpButton.style.display = 'none';
      jumpButton.classList.remove('has-new');
    }
  });
}

/**
 * Scroll to bottom only if user hasn't scrolled up
 * Call this after adding new messages
 * @param {boolean} force - Force scroll even if user scrolled up
 */
export function scrollToBottomIfNeeded(force = false) {
  if (!chatArea) return;

  // Re-check position in case content was added
  const { scrollTop, scrollHeight, clientHeight } = chatArea;
  const distanceFromBottom = scrollHeight - scrollTop - clientHeight;
  const wasAtBottom = distanceFromBottom <= scrollThreshold + 50; // Slight buffer

  if (force || wasAtBottom || !isUserScrolledUp) {
    scrollToBottom(true);
  } else {
    // User is reading history, update button visibility
    checkScrollPosition();
  }
}

/**
 * Show the jump button with a "New messages" indicator
 * Call this when new messages arrive while user is scrolled up
 */
export function showNewMessageIndicator() {
  if (jumpButton && isUserScrolledUp) {
    jumpButton.classList.add('has-new');
    jumpButton.style.display = 'flex';
  }
}

/**
 * Clean up the auto-scroll module
 */
export function cleanupChatAutoScroll() {
  // Cancel pending RAF
  if (scrollRAF) {
    cancelAnimationFrame(scrollRAF);
    scrollRAF = null;
  }

  if (chatArea) {
    chatArea.removeEventListener('scroll', handleScroll, { passive: true });
  }

  if (jumpButton) {
    jumpButton.removeEventListener('click', handleJumpClick);
    jumpButton.remove();
    jumpButton = null;
  }

  chatArea = null;
  isUserScrolledUp = false;

  // Clean up global references
  delete window.scrollChatToBottom;
  delete window.scrollChatToBottomIfNeeded;
}

/**
 * Check if the user is currently scrolled up
 * @returns {boolean}
 */
export function isScrolledUp() {
  return isUserScrolledUp;
}

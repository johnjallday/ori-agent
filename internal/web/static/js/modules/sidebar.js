// Sidebar Controller Module
// Main sidebar functionality coordinator that orchestrates all sidebar modules

// Track auto-compact state (not persisted)
let autoCompacted = false;

// Main sidebar setup function that coordinates all modules
function setupSidebar() {
  console.log('Setting up sidebar functionality...');

  // Add hover effects to interactive items
  document.querySelectorAll('.agent-item, .plugin-item').forEach(item => {
    item.addEventListener('mouseenter', () => {
      if (!item.style.background.includes('var(--primary-color-light)')) {
        item.style.background = 'var(--bg-tertiary)';
      }
    });

    item.addEventListener('mouseleave', () => {
      if (!item.style.background.includes('var(--primary-color-light)')) {
        item.style.background = 'var(--bg-secondary)';
      }
    });
  });

  // Initialize compact mode
  initCompactMode();

  console.log('Sidebar setup complete');
}

/**
 * Initialize compact mode functionality
 * - Manual toggle via button
 * - Auto-collapse when session sidebar opens
 * - Keyboard shortcut (Ctrl+B)
 * - Persist user preference to localStorage
 */
function initCompactMode() {
  const toggle = document.getElementById('compactModeToggle');
  const sidebar = document.getElementById('sidebar');
  const sessionSidebar = document.getElementById('sessionSidebar');

  if (!sidebar) return;

  // Restore user's manual preference
  const userPreferCompact = localStorage.getItem('sidebarCompact') === 'true';
  if (userPreferCompact) {
    sidebar.classList.add('compact');
    updateSidebarWidth(true);
  }

  // Manual toggle button
  toggle?.addEventListener('click', () => {
    toggleCompactMode(false);
  });

  // Keyboard shortcut: Ctrl+B (or Cmd+B on Mac)
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'b') {
      // Don't interfere if user is typing in an input
      if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.isContentEditable) {
        return;
      }
      e.preventDefault();
      toggleCompactMode(false);
    }
  });

  // Auto-collapse when session sidebar opens (if screen < 1400px and user hasn't chosen compact)
  if (sessionSidebar) {
    const observer = new MutationObserver(() => {
      checkAutoCompact(sidebar, sessionSidebar);
    });
    observer.observe(sessionSidebar, { attributes: true, attributeFilter: ['class'] });

    // Also check on window resize
    window.addEventListener('resize', () => {
      checkAutoCompact(sidebar, sessionSidebar);
    });

    // Initial check
    checkAutoCompact(sidebar, sessionSidebar);
  }
}

/**
 * Toggle compact mode manually
 * @param {boolean} isAuto - Whether this is an auto-toggle (don't persist)
 */
function toggleCompactMode(isAuto = false) {
  const sidebar = document.getElementById('sidebar');
  if (!sidebar) return;

  sidebar.classList.toggle('compact');
  const nowCompact = sidebar.classList.contains('compact');

  if (!isAuto) {
    // User made explicit choice - persist it
    localStorage.setItem('sidebarCompact', nowCompact);
    autoCompacted = false;
  }

  updateSidebarWidth(nowCompact);

  // Dispatch event for other components
  window.dispatchEvent(new CustomEvent('sidebarCompactChange', {
    detail: { compact: nowCompact, auto: isAuto }
  }));
}

/**
 * Check if we should auto-compact the sidebar
 */
function checkAutoCompact(sidebar, sessionSidebar) {
  if (!sidebar || !sessionSidebar) return;

  const sessionVisible = !sessionSidebar.classList.contains('d-none');
  const userWantsCompact = localStorage.getItem('sidebarCompact') === 'true';
  const isCurrentlyCompact = sidebar.classList.contains('compact');

  // Only auto-compact if:
  // 1. Session sidebar is visible
  // 2. User hasn't explicitly chosen compact mode
  // 3. Screen is less than 1400px wide
  // 4. Sidebar is not already compact
  if (sessionVisible && !userWantsCompact && window.innerWidth < 1400 && !isCurrentlyCompact) {
    sidebar.classList.add('compact');
    autoCompacted = true;
    updateSidebarWidth(true);
  } else if (!sessionVisible && autoCompacted) {
    // Session sidebar closed - restore if we auto-compacted
    sidebar.classList.remove('compact');
    autoCompacted = false;
    updateSidebarWidth(false);
  }
}

/**
 * Update the CSS variable for sidebar width
 */
function updateSidebarWidth(isCompact) {
  const compactWidth = 60;
  const normalWidth = parseInt(localStorage.getItem('sidebarWidth')) || 300;
  const width = isCompact ? compactWidth : normalWidth;
  document.documentElement.style.setProperty('--sidebar-width', `${width}px`);
}

// Initialize all sidebar modules and load data
async function initializeSidebar() {
  try {
    console.log('Initializing sidebar modules...');

    // Load initial data for each module
    if (typeof loadAgentsForSidebar === 'function') {
      await loadAgentsForSidebar();
    }

    if (typeof loadPluginsForSidebar === 'function') {
      await loadPluginsForSidebar();
    }

    console.log('All sidebar modules initialized successfully');
  } catch (error) {
    console.error('Error initializing sidebar modules:', error);
  }
}

// Initialize sidebar when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', async () => {
    setupSidebar();
    await initializeSidebar();
  });
} else {
  setupSidebar();
  initializeSidebar();
}
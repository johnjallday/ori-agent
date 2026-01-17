// Sidebar Controller Module
// Main sidebar functionality coordinator that orchestrates all sidebar modules

const sidebarLog = Logger.withContext('Sidebar');

// Main sidebar setup function that coordinates all modules
function setupSidebar() {
  sidebarLog.debug('Setting up sidebar functionality...');

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

  sidebarLog.debug('Sidebar setup complete');
}

// Initialize all sidebar modules and load data
async function initializeSidebar() {
  try {
    sidebarLog.debug('Initializing sidebar modules...');

    // Load initial data for each module
    if (typeof loadAgentsForSidebar === 'function') {
      await loadAgentsForSidebar();
    }


    sidebarLog.info('All sidebar modules initialized successfully');
    EventBus.emit('sidebar:initialized');
  } catch (error) {
    sidebarLog.error('Error initializing sidebar modules:', error);
    EventBus.emit('sidebar:error', { error: error.message });
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

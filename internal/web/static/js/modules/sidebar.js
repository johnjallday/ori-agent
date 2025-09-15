// Sidebar Controller Module
// Main sidebar functionality coordinator that orchestrates all sidebar modules

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

  console.log('Sidebar setup complete');
}

// Initialize all sidebar modules and load data
async function initializeSidebar() {
  try {
    console.log('Initializing sidebar modules...');

    // Load initial data for each module
    if (typeof loadAgents === 'function') {
      await loadAgents();
    }
    
    if (typeof loadPlugins === 'function') {
      await loadPlugins();
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
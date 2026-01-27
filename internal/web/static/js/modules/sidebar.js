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

// Sidebar toggle and responsive behavior
function setupSidebarToggle() {
  const sidebarToggle = document.getElementById('sidebarToggle');
  const sidebar = document.getElementById('sidebar');

  if (!sidebarToggle || !sidebar) {
    return;
  }

  const defaultMode = document.body?.dataset?.sidebarDefault || '';
  const preferVisible = defaultMode === 'visible';
  const preferHidden = defaultMode === 'hidden';

  const isEditableTarget = (target) => {
    if (!target) return false;
    const tagName = target.tagName;
    return target.isContentEditable || tagName === 'INPUT' || tagName === 'TEXTAREA' || tagName === 'SELECT';
  };

  const setSidebarWidth = (hidden) => {
    if (hidden) {
      document.documentElement.style.setProperty('--sidebar-width', '0px');
      return;
    }
    const savedWidth = localStorage.getItem('sidebarWidth') || '300';
    document.documentElement.style.setProperty('--sidebar-width', `${savedWidth}px`);
  };

  const showSidebar = (emitEvent) => {
    if (window.innerWidth >= 992) {
      sidebar.classList.add('d-lg-block');
      sidebar.classList.remove('sidebar-mobile-show');
    } else {
      sidebar.classList.remove('d-lg-block');
      sidebar.classList.add('sidebar-mobile-show');
    }
    sidebar.classList.remove('d-none');
    sidebarToggle.setAttribute('aria-expanded', 'true');
    setSidebarWidth(false);

    if (emitEvent && window.EventBus) {
      EventBus.emit('sidebar:toggled', { hidden: false });
    }
  };

  const hideSidebar = (emitEvent) => {
    sidebar.classList.add('d-none');
    sidebar.classList.remove('d-lg-block');
    sidebar.classList.remove('sidebar-mobile-show');
    sidebarToggle.setAttribute('aria-expanded', 'false');
    setSidebarWidth(true);

    if (emitEvent && window.EventBus) {
      EventBus.emit('sidebar:toggled', { hidden: true });
    }
  };

  sidebarToggle.addEventListener('click', function(event) {
    event.preventDefault();
    event.stopPropagation();

    const isHidden = sidebar.classList.contains('d-none');
    if (isHidden) {
      showSidebar(true);
    } else {
      hideSidebar(true);
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
    const isClickInModal = event.target.closest('.modal') || event.target.classList.contains('modal-backdrop');

    if (!isClickInSidebar && !isClickOnToggle && !isClickInModal &&
        !sidebar.classList.contains('d-none') &&
        window.innerWidth < 992) {
      hideSidebar(false);
    }
  });

  function handleSidebarResponsive() {
    if (window.innerWidth >= 992) {
      if (preferHidden) {
        hideSidebar(false);
        return;
      }

      if (sidebar.classList.contains('d-none')) {
        if (preferVisible) {
          showSidebar(false);
        } else {
          hideSidebar(false);
        }
        return;
      }

      sidebar.classList.add('d-lg-block');
      sidebar.classList.remove('sidebar-mobile-show');
      sidebarToggle.setAttribute('aria-expanded', 'true');
      setSidebarWidth(false);
    } else {
      hideSidebar(false);
    }
  }

  window.addEventListener('resize', handleSidebarResponsive);
  handleSidebarResponsive();
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
    setupSidebarToggle();
    await initializeSidebar();
  });
} else {
  setupSidebar();
  setupSidebarToggle();
  initializeSidebar();
}

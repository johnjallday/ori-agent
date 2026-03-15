// Sidebar Controller Module
// Main sidebar functionality coordinator that orchestrates all sidebar modules

const sidebarLog = Logger.withContext('Sidebar');
const ASSISTANT_XP_PER_LEVEL = 100;

function isEvolutionEnabled() {
  return Boolean(window.oriFeatures?.evolutionEnabled);
}

function normalizeAssistantProgress(progress) {
  const safe = progress || {};
  const experience = Number.isFinite(Number(safe.experience)) ? Math.max(0, Number(safe.experience)) : 0;
  const level = Number.isFinite(Number(safe.level)) ? Math.max(0, Number(safe.level)) : 0;
  const rank = typeof safe.rank === 'string' && safe.rank.trim() ? safe.rank.trim() : 'novice';
  return { level, experience, rank };
}

function toTitleCase(value) {
  return String(value || '')
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(' ');
}

function formatNumber(value) {
  return new Intl.NumberFormat().format(Number(value || 0));
}

function renderAssistantProgress(progress) {
  const widget = document.getElementById('assistantProgressWidget');
  const rankBadge = document.getElementById('assistantRankBadge');
  const levelValue = document.getElementById('assistantLevelValue');
  const xpValue = document.getElementById('assistantXpValue');
  const progressBar = document.getElementById('assistantXpProgressBar');
  if (!widget || !rankBadge || !levelValue || !xpValue || !progressBar) {
    return;
  }

  const normalized = normalizeAssistantProgress(progress);
  const progressWithinLevel = normalized.experience % ASSISTANT_XP_PER_LEVEL;
  const progressPercent = Math.min(100, Math.max(0, Math.round((progressWithinLevel / ASSISTANT_XP_PER_LEVEL) * 100)));

  rankBadge.textContent = toTitleCase(normalized.rank);
  levelValue.textContent = `Assistant Level ${normalized.level}`;
  xpValue.textContent = `${formatNumber(normalized.experience)} XP`;
  progressBar.style.width = `${progressPercent}%`;
  progressBar.setAttribute('aria-valuenow', String(progressPercent));

  widget.classList.remove('d-none');
}

async function loadAssistantProgressForSidebar() {
  const widget = document.getElementById('assistantProgressWidget');
  if (!widget) {
    return;
  }
  if (!isEvolutionEnabled()) {
    widget.classList.add('d-none');
    return;
  }
  if (typeof API === 'undefined' || typeof API.get !== 'function') {
    return;
  }

  try {
    const data = await API.get('/api/evolution/assistant');
    if (!data?.assistant) {
      widget.classList.add('d-none');
      return;
    }
    renderAssistantProgress(data.assistant);
  } catch (error) {
    sidebarLog.debug('Assistant progression unavailable', { error: error?.message || error });
    widget.classList.add('d-none');
  }
}

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

function syncNavbarOffset() {
  const root = document.documentElement;
  const navbar = document.querySelector('.navbar');

  if (!root || !navbar) {
    return;
  }

  let frameId = null;

  const updateOffset = () => {
    frameId = null;
    const navbarHeight = Math.max(76, Math.ceil(navbar.getBoundingClientRect().height));
    root.style.setProperty('--navbar-offset', `${navbarHeight}px`);
  };

  const scheduleOffsetUpdate = () => {
    if (frameId !== null) {
      cancelAnimationFrame(frameId);
    }
    frameId = requestAnimationFrame(updateOffset);
  };

  scheduleOffsetUpdate();
  window.addEventListener('load', scheduleOffsetUpdate, { once: true });
  window.addEventListener('resize', scheduleOffsetUpdate);

  if (typeof ResizeObserver === 'function') {
    const navbarObserver = new ResizeObserver(scheduleOffsetUpdate);
    navbarObserver.observe(navbar);
  }
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

    await loadAssistantProgressForSidebar();

    sidebarLog.info('All sidebar modules initialized successfully');
    EventBus.emit('sidebar:initialized');

    if (isEvolutionEnabled()) {
      window.setInterval(() => {
        loadAssistantProgressForSidebar();
      }, 60000);
    }
  } catch (error) {
    sidebarLog.error('Error initializing sidebar modules:', error);
    EventBus.emit('sidebar:error', { error: error.message });
  }
}

// Initialize sidebar when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', async () => {
    syncNavbarOffset();
    setupSidebar();
    setupSidebarToggle();
    await initializeSidebar();
  });
} else {
  syncNavbarOffset();
  setupSidebar();
  setupSidebarToggle();
  initializeSidebar();
}

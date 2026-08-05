/**
 * Settings Navigation Module
 * Handles sidebar navigation, search filtering, active state tracking,
 * smooth scrolling, and URL hash synchronization for the settings page.
 */

const SettingsNavigation = (function () {
  // DOM elements
  let searchInput = null;
  let navItems = [];
  let navGroups = [];
  let sections = [];
  let noResultsEl = null;

  // Configuration
  const SCROLL_OFFSET = 80; // Offset for fixed header
  const DEBOUNCE_DELAY = 150;

  /**
   * Initialize the settings navigation
   */
  function init() {
    // Get DOM elements
    searchInput = document.getElementById('settingsSearch');
    navItems = Array.from(document.querySelectorAll('.settings-nav-item'));
    navGroups = Array.from(document.querySelectorAll('.settings-nav-group'));
    sections = Array.from(document.querySelectorAll('.settings-section'));
    noResultsEl = document.getElementById('settingsNoResults');

    if (!searchInput || navItems.length === 0) {
      console.warn('Settings navigation: Required elements not found');
      return;
    }

    // Setup event listeners
    setupSearchListener();
    setupNavClickListeners();
    setupScrollObserver();
    handleInitialHash();

    // Listen for hash changes
    window.addEventListener('hashchange', handleHashChange);
  }

  /**
   * Setup search input listener with debounce
   */
  function setupSearchListener() {
    let debounceTimer;

    searchInput.addEventListener('input', function () {
      clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        filterNavigation(this.value.trim().toLowerCase());
      }, DEBOUNCE_DELAY);
    });

    // Clear search on Escape
    searchInput.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') {
        this.value = '';
        filterNavigation('');
        this.blur();
      }
    });
  }

  /**
   * Filter navigation items based on search query
   */
  function filterNavigation(query) {
    if (!query) {
      // Show all items and groups
      navItems.forEach(item => item.classList.remove('hidden'));
      navGroups.forEach(group => group.classList.remove('hidden'));
      if (noResultsEl) noResultsEl.classList.remove('visible');
      return;
    }

    let hasResults = false;

    // Check each nav item
    navItems.forEach(item => {
      const text = item.querySelector('.settings-nav-item-text').textContent.toLowerCase();
      const sectionId = item.getAttribute('data-section');
      const section = sectionId ? document.getElementById(sectionId) : null;

      // Also check section content for matches
      let sectionMatches = false;
      if (section) {
        const sectionText = section.textContent.toLowerCase();
        sectionMatches = sectionText.includes(query);
      }

      if (text.includes(query) || sectionMatches) {
        item.classList.remove('hidden');
        hasResults = true;
      } else {
        item.classList.add('hidden');
      }
    });

    // Hide groups that have no visible items
    navGroups.forEach(group => {
      const visibleItems = group.querySelectorAll('.settings-nav-item:not(.hidden)');
      group.classList.toggle('hidden', visibleItems.length === 0);
    });

    // Show/hide no results message
    if (noResultsEl) noResultsEl.classList.toggle('visible', !hasResults);
  }

  /**
   * Setup click listeners for nav items
   */
  function setupNavClickListeners() {
    navItems.forEach(item => {
      item.addEventListener('click', function (e) {
        const sectionId = this.getAttribute('data-section');
        const isExternalLink = this.dataset.externalLink === 'true';
        if (!sectionId || isExternalLink) {
          return;
        }

        e.preventDefault();
        scrollToSection(sectionId);
      });
    });
  }

  /**
   * Scroll to a section with smooth behavior
   */
  function scrollToSection(sectionId) {
    const section = document.getElementById(sectionId);
    if (!section) return;

    const settingsContent = document.querySelector('.settings-content');
    if (!settingsContent) return;

    // Calculate the scroll position relative to the scroll container
    const sectionRect = section.getBoundingClientRect();
    const contentRect = settingsContent.getBoundingClientRect();

    // Current scroll position plus the offset from the top of the container
    const scrollTop =
      settingsContent.scrollTop + (sectionRect.top - contentRect.top) - SCROLL_OFFSET;

    settingsContent.scrollTo({
      top: Math.max(0, scrollTop),
      behavior: 'smooth'
    });

    // Update URL hash without triggering scroll
    history.pushState(null, '', `#${sectionId}`);

    // Update active state
    setActiveNavItem(sectionId);
  }

  /**
   * Set active state on nav item
   */
  function setActiveNavItem(sectionId) {
    navItems.forEach(item => {
      const itemSection = item.getAttribute('data-section');
      item.classList.toggle('active', itemSection === sectionId);
    });
  }

  /**
   * Setup Intersection Observer for scroll tracking
   */
  function setupScrollObserver() {
    const settingsContent = document.querySelector('.settings-content');
    if (!settingsContent) return;

    // Use a throttled scroll handler for better performance
    let ticking = false;

    settingsContent.addEventListener('scroll', function () {
      if (!ticking) {
        requestAnimationFrame(() => {
          updateActiveOnScroll();
          ticking = false;
        });
        ticking = true;
      }
    });
  }

  /**
   * Update active nav item based on scroll position
   */
  function updateActiveOnScroll() {
    const settingsContent = document.querySelector('.settings-content');
    if (!settingsContent) return;

    const contentRect = settingsContent.getBoundingClientRect();
    let currentSection = null;

    // Find the section that's currently at or above the top of the visible area
    sections.forEach(section => {
      const sectionRect = section.getBoundingClientRect();
      // Check if section top is at or above the threshold (top of content + offset)
      if (sectionRect.top <= contentRect.top + SCROLL_OFFSET + 50) {
        currentSection = section.id;
      }
    });

    if (currentSection) {
      setActiveNavItem(currentSection);
    }
  }

  /**
   * Handle initial URL hash on page load
   */
  function handleInitialHash() {
    const hash = window.location.hash.slice(1);
    if (hash) {
      // Wait for DOM to be fully rendered
      setTimeout(() => {
        scrollToSection(hash);
      }, 100);
    } else {
      // Set first section as active by default
      if (navItems.length > 0) {
        const firstSection = navItems[0].getAttribute('data-section');
        setActiveNavItem(firstSection);
      }
    }
  }

  /**
   * Handle URL hash changes
   */
  function handleHashChange() {
    const hash = window.location.hash.slice(1);
    if (hash) {
      scrollToSection(hash);
    }
  }

  /**
   * Public method to navigate to a section programmatically
   */
  function navigateTo(sectionId) {
    scrollToSection(sectionId);
  }

  /**
   * Public method to clear search and show all items
   */
  function clearSearch() {
    if (searchInput) {
      searchInput.value = '';
      filterNavigation('');
    }
  }

  // Initialize when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // Public API
  return {
    init,
    navigateTo,
    clearSearch
  };
})();

// Export for use in other modules
if (typeof window !== 'undefined') {
  window.SettingsNavigation = SettingsNavigation;
}

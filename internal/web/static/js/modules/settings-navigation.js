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
  let disclosure = null;
  let narrowLayout = null;

  // Inset within the desktop scroller, or below the measured mobile navbar.
  const SCROLL_OFFSET = 16;
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

    disclosure = document.getElementById('settingsNavDisclosure');
    narrowLayout = window.matchMedia('(max-width: 991px)');
    const syncDisclosure = () => {
      if (disclosure) disclosure.open = !narrowLayout.matches;
    };
    syncDisclosure();
    narrowLayout.addEventListener('change', syncDisclosure);

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

    // Typing reveals results without requiring a second disclosure action.
    if (disclosure && narrowLayout.matches) disclosure.open = true;
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
        scrollToSection(sectionId, { moveFocus: true });
      });
    });
  }

  /**
   * Scroll to a section with smooth behavior
   */
  function navbarOffset() {
    return (
      parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')) ||
      0
    );
  }

  function scrollToSection(sectionId, { moveFocus = false, updateHistory = true } = {}) {
    const section = document.getElementById(sectionId);
    const settingsContent = document.querySelector('.settings-content');
    if (!section || !settingsContent) return;

    const narrow = narrowLayout.matches;
    // Close before measuring: the mobile page uses one document scroller.
    if (narrow && disclosure) disclosure.open = false;
    const sectionRect = section.getBoundingClientRect();
    const contentRect = settingsContent.getBoundingClientRect();
    const scrollTop = narrow
      ? window.scrollY + sectionRect.top - navbarOffset() - SCROLL_OFFSET
      : settingsContent.scrollTop + sectionRect.top - contentRect.top - SCROLL_OFFSET;
    (narrow ? window : settingsContent).scrollTo({
      top: Math.max(0, scrollTop),
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth'
    });

    // Do not append history during Back/Forward or an initial deep link.
    if (updateHistory && window.location.hash !== `#${sectionId}`) {
      history.pushState(null, '', `#${sectionId}`);
    }
    setActiveNavItem(sectionId);

    // A selected link must not retain focus inside the now-closed disclosure.
    if (narrow && moveFocus) {
      const heading = section.querySelector('h2');
      if (heading) {
        heading.setAttribute('tabindex', '-1');
        heading.focus({ preventScroll: true });
      }
    }
  }

  /**
   * Set active state on nav item
   */
  function setActiveNavItem(sectionId) {
    navItems.forEach(item => {
      const itemSection = item.getAttribute('data-section');
      const active = !!itemSection && itemSection === sectionId;
      item.classList.toggle('active', active);
      if (active) item.setAttribute('aria-current', 'location');
      else item.removeAttribute('aria-current');
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

    const onScroll = () => {
      if (!ticking) {
        requestAnimationFrame(() => {
          updateActiveOnScroll();
          ticking = false;
        });
        ticking = true;
      }
    };
    settingsContent.addEventListener('scroll', onScroll);
    window.addEventListener('scroll', onScroll);
  }

  /**
   * Update active nav item based on scroll position
   */
  function updateActiveOnScroll() {
    const settingsContent = document.querySelector('.settings-content');
    if (!settingsContent) return;

    const top = narrowLayout.matches ? navbarOffset() : settingsContent.getBoundingClientRect().top;
    let currentSection = null;

    // Find the section that's currently at or above the top of the visible area
    sections.forEach(section => {
      const sectionRect = section.getBoundingClientRect();
      // Check if section top is at or above the threshold (top of content + offset)
      if (sectionRect.top <= top + SCROLL_OFFSET + 50) {
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
        scrollToSection(hash, { updateHistory: false });
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
      scrollToSection(hash, { updateHistory: false });
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

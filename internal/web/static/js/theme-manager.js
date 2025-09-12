/**
 * Theme management for dark/light mode switching
 */

'use strict';

class ThemeManager {
  constructor() {
    this.init();
  }

  /**
   * Apply theme to the document
   * @param {boolean} isDark - Whether to apply dark theme
   */
  applyTheme(isDark) {
    // Bootstrap theming
    document.documentElement.setAttribute('data-bs-theme', isDark ? 'dark' : 'light');
    // Custom theme overrides
    document.documentElement.classList.toggle('dark-mode', isDark);
    // Persist to localStorage
    localStorage.setItem('darkMode', String(isDark));
  }

  /**
   * Toggle between dark and light themes
   */
  toggleTheme() {
    const next = !(localStorage.getItem('darkMode') === 'true');
    this.applyTheme(next);
  }

  /**
   * Get current theme state
   * @returns {boolean} True if dark mode is enabled
   */
  isDarkMode() {
    return localStorage.getItem('darkMode') === 'true';
  }

  /**
   * Initialize theme from localStorage and set up event listeners
   */
  init() {
    // Init theme from storage (default light)
    const storedDark = localStorage.getItem('darkMode') === 'true';
    this.applyTheme(storedDark);

    // Set up toggle button listener
    const toggleButton = document.getElementById('darkModeToggle');
    if (toggleButton) {
      toggleButton.addEventListener('click', () => {
        this.toggleTheme();
      });
    }
  }
}

// Export for module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = ThemeManager;
}

// Initialize theme manager when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    window.themeManager = new ThemeManager();
  });
} else {
  window.themeManager = new ThemeManager();
}
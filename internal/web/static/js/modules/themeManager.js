/**
 * Theme Manager Module
 * Handles dark mode, theme switching, and theme persistence
 */

class ThemeManager {
  constructor() {
    this.currentTheme = 'light';
    this.storageKey = 'dolphin-theme';
    this.observers = [];
  }

  initialize() {
    console.log('ThemeManager.initialize() called');
    
    // Load saved theme or detect system preference
    this.loadTheme();
    
    // Setup theme toggle button
    this.setupThemeToggle();
    
    // Listen for system theme changes
    this.setupSystemThemeListener();
    
    console.log('Theme manager initialized with theme:', this.currentTheme);
  }

  loadTheme() {
    // Check localStorage first
    const savedTheme = localStorage.getItem(this.storageKey);
    
    if (savedTheme) {
      this.currentTheme = savedTheme;
    } else {
      // Detect system preference
      this.currentTheme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    
    this.applyTheme(this.currentTheme);
  }

  setupThemeToggle() {
    console.log('Setting up theme toggle...');
    const toggleButton = document.getElementById('darkModeToggle');
    console.log('Dark mode button found:', toggleButton);
    if (toggleButton) {
      toggleButton.addEventListener('click', () => {
        console.log('Dark mode button clicked!');
        this.toggleTheme();
      });
      console.log('Click event listener added to dark mode button');
    } else {
      console.error('Dark mode button not found!');
    }
  }

  setupSystemThemeListener() {
    // Listen for system theme changes
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    mediaQuery.addEventListener('change', (e) => {
      // Only follow system theme if user hasn't manually set a theme
      if (!localStorage.getItem(this.storageKey)) {
        const systemTheme = e.matches ? 'dark' : 'light';
        this.setTheme(systemTheme);
      }
    });
  }

  toggleTheme() {
    const newTheme = this.currentTheme === 'light' ? 'dark' : 'light';
    this.setTheme(newTheme);
  }

  setTheme(theme) {
    if (theme !== 'light' && theme !== 'dark') {
      console.warn('Invalid theme:', theme);
      return;
    }

    this.currentTheme = theme;
    
    // Save to localStorage
    localStorage.setItem(this.storageKey, theme);
    
    // Apply theme
    this.applyTheme(theme);
    
    // Notify observers
    this.notifyObservers(theme);
    
    console.log('Theme changed to:', theme);
  }

  applyTheme(theme) {
    const html = document.documentElement;
    const body = document.body;
    
    if (theme === 'dark') {
      html.setAttribute('data-bs-theme', 'dark');
      body.classList.add('dark-mode');
      body.classList.remove('bg-light');
      body.classList.add('bg-dark');
    } else {
      html.setAttribute('data-bs-theme', 'light');
      body.classList.remove('dark-mode');
      body.classList.remove('bg-dark');
      body.classList.add('bg-light');
    }

    // Update theme toggle button text
    this.updateToggleButton(theme);
  }

  updateToggleButton(theme) {
    const toggleButton = document.getElementById('darkModeToggle');
    if (toggleButton) {
      const span = toggleButton.querySelector('span');
      if (span) {
        span.textContent = theme === 'dark' ? 'Light' : 'Dark';
      }
      
      // Update SVG icon
      const svg = toggleButton.querySelector('svg');
      if (svg) {
        const path = svg.querySelector('path');
        if (path) {
          if (theme === 'dark') {
            // Sun icon for light mode button
            path.setAttribute('d', 'M12 17.27L18.18 21L16.54 13.97L22 9.24L14.81 8.62L12 2L9.19 8.62L2 9.24L7.46 13.97L5.82 21L12 17.27Z');
          } else {
            // Moon icon for dark mode button  
            path.setAttribute('d', 'M12 3c-4.97 0-9 4.03-9 9s4.03 9 9 9 9-4.03 9-9c0-.46-.04-.92-.1-1.36-.98 1.37-2.58 2.26-4.4 2.26-2.98 0-5.4-2.42-5.4-5.4 0-1.81.89-3.42 2.26-4.4-.44-.06-.9-.1-1.36-.1z');
          }
        }
      }
    }
  }

  getCurrentTheme() {
    return this.currentTheme;
  }

  isDarkMode() {
    return this.currentTheme === 'dark';
  }

  // Observer pattern for theme changes
  addObserver(callback) {
    this.observers.push(callback);
  }

  removeObserver(callback) {
    this.observers = this.observers.filter(obs => obs !== callback);
  }

  notifyObservers(theme) {
    this.observers.forEach(callback => {
      try {
        callback(theme);
      } catch (error) {
        console.error('Error in theme observer:', error);
      }
    });
  }

  // CSS custom property helpers
  getCSSVariable(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  setCSSVariable(name, value) {
    document.documentElement.style.setProperty(name, value);
  }

  // Helper to get theme-appropriate colors
  getThemeColors() {
    return {
      primary: this.getCSSVariable('--primary-color') || (this.isDarkMode() ? '#007bff' : '#0d6efd'),
      secondary: this.getCSSVariable('--secondary-color') || (this.isDarkMode() ? '#6c757d' : '#6c757d'),
      success: this.getCSSVariable('--success-color') || (this.isDarkMode() ? '#198754' : '#28a745'),
      danger: this.getCSSVariable('--danger-color') || (this.isDarkMode() ? '#dc3545' : '#dc3545'),
      warning: this.getCSSVariable('--warning-color') || (this.isDarkMode() ? '#ffc107' : '#ffc107'),
      info: this.getCSSVariable('--info-color') || (this.isDarkMode() ? '#0dcaf0' : '#17a2b8'),
      background: this.getCSSVariable('--bg-primary') || (this.isDarkMode() ? '#212529' : '#ffffff'),
      surface: this.getCSSVariable('--bg-secondary') || (this.isDarkMode() ? '#343a40' : '#f8f9fa'),
      text: this.getCSSVariable('--text-primary') || (this.isDarkMode() ? '#ffffff' : '#212529'),
      textSecondary: this.getCSSVariable('--text-secondary') || (this.isDarkMode() ? '#adb5bd' : '#6c757d'),
      border: this.getCSSVariable('--border-color') || (this.isDarkMode() ? '#495057' : '#dee2e6')
    };
  }
}

// Export singleton instance
const themeManager = new ThemeManager();
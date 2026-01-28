/**
 * Settings Controller Module
 * Provides unified state management, toast notifications, and module coordination
 * for the settings page.
 */

const SettingsController = (function() {
  // State
  let initialized = false;
  const modules = new Map();
  const eventHandlers = new Map();

  /**
   * Initialize the settings controller
   */
  function init() {
    if (initialized) return;
    initialized = true;

    // Ensure Toast is initialized
    if (typeof Toast !== 'undefined' && Toast.init) {
      Toast.init();
    }

    // Initialize navigation module if available
    if (typeof SettingsNavigation !== 'undefined') {
      registerModule('navigation', SettingsNavigation);
    }

    console.log('SettingsController initialized');
  }

  /**
   * Register a module with the controller
   * @param {string} name - Module name
   * @param {object} module - Module instance with optional init() method
   */
  function registerModule(name, module) {
    modules.set(name, module);
    if (module.init && typeof module.init === 'function') {
      module.init();
    }
  }

  /**
   * Get a registered module
   * @param {string} name - Module name
   * @returns {object|undefined} The module instance
   */
  function getModule(name) {
    return modules.get(name);
  }

  /**
   * Show a toast notification
   * @param {string} message - The message to display
   * @param {string} type - Toast type: 'success', 'error', 'warning', 'info'
   * @param {object} options - Additional options (title, duration)
   */
  function notify(message, type = 'info', options = {}) {
    if (typeof Toast !== 'undefined') {
      switch (type) {
        case 'success':
          Toast.success(message, options);
          break;
        case 'error':
          Toast.error(message, options);
          break;
        case 'warning':
          Toast.warning(message, options);
          break;
        default:
          Toast.info(message, options);
      }
    } else {
      // Fallback to alert if Toast is not available
      const prefix = type === 'error' ? 'Error: ' : '';
      alert(prefix + message);
    }
  }

  /**
   * Show a success notification
   * @param {string} message - The message to display
   * @param {object} options - Additional options
   */
  function notifySuccess(message, options = {}) {
    notify(message, 'success', options);
  }

  /**
   * Show an error notification
   * @param {string} message - The message to display
   * @param {object} options - Additional options
   */
  function notifyError(message, options = {}) {
    notify(message, 'error', options);
  }

  /**
   * Show a warning notification
   * @param {string} message - The message to display
   * @param {object} options - Additional options
   */
  function notifyWarning(message, options = {}) {
    notify(message, 'warning', options);
  }

  /**
   * Show an info notification
   * @param {string} message - The message to display
   * @param {object} options - Additional options
   */
  function notifyInfo(message, options = {}) {
    notify(message, 'info', options);
  }

  /**
   * Subscribe to an event
   * @param {string} event - Event name
   * @param {function} handler - Event handler
   */
  function on(event, handler) {
    if (!eventHandlers.has(event)) {
      eventHandlers.set(event, []);
    }
    eventHandlers.get(event).push(handler);
  }

  /**
   * Unsubscribe from an event
   * @param {string} event - Event name
   * @param {function} handler - Event handler to remove
   */
  function off(event, handler) {
    if (!eventHandlers.has(event)) return;
    const handlers = eventHandlers.get(event);
    const index = handlers.indexOf(handler);
    if (index > -1) {
      handlers.splice(index, 1);
    }
  }

  /**
   * Emit an event
   * @param {string} event - Event name
   * @param {any} data - Event data
   */
  function emit(event, data) {
    if (!eventHandlers.has(event)) return;
    eventHandlers.get(event).forEach(handler => {
      try {
        handler(data);
      } catch (error) {
        console.error(`Error in event handler for ${event}:`, error);
      }
    });
  }

  /**
   * Make an API request with standardized error handling
   * @param {string} url - API endpoint
   * @param {object} options - Fetch options
   * @returns {Promise<any>} Response data
   */
  async function apiRequest(url, options = {}) {
    const defaultOptions = {
      headers: {
        'Content-Type': 'application/json',
        'X-Requested-With': 'XMLHttpRequest'
      }
    };

    const mergedOptions = {
      ...defaultOptions,
      ...options,
      headers: {
        ...defaultOptions.headers,
        ...options.headers
      }
    };

    try {
      const response = await fetch(url, mergedOptions);

      if (!response.ok) {
        let errorMessage;
        try {
          const errorData = await response.json();
          errorMessage = errorData.error || errorData.message || response.statusText;
        } catch {
          errorMessage = await response.text() || response.statusText;
        }
        throw new Error(errorMessage);
      }

      // Return JSON if content-type is JSON, otherwise return text
      const contentType = response.headers.get('content-type');
      if (contentType && contentType.includes('application/json')) {
        return await response.json();
      }
      return await response.text();
    } catch (error) {
      console.error(`API request failed: ${url}`, error);
      throw error;
    }
  }

  /**
   * Set loading state on a button
   * @param {HTMLElement} button - Button element
   * @param {boolean} loading - Loading state
   * @param {string} loadingText - Text to show while loading
   */
  function setButtonLoading(button, loading, loadingText = 'Loading...') {
    if (!button) return;

    if (loading) {
      button._originalHTML = button.innerHTML;
      button._originalDisabled = button.disabled;
      button.disabled = true;
      button.innerHTML = `<span class="spinner-border spinner-border-sm me-2"></span>${loadingText}`;
    } else {
      button.disabled = button._originalDisabled || false;
      button.innerHTML = button._originalHTML || button.innerHTML;
    }
  }

  /**
   * Navigate to a settings section
   * @param {string} sectionId - Section ID to navigate to
   */
  function navigateTo(sectionId) {
    const navigation = getModule('navigation');
    if (navigation && navigation.navigateTo) {
      navigation.navigateTo(sectionId);
    } else {
      // Fallback: update URL hash
      window.location.hash = sectionId;
    }
  }

  /**
   * Clear the search and show all navigation items
   */
  function clearSearch() {
    const navigation = getModule('navigation');
    if (navigation && navigation.clearSearch) {
      navigation.clearSearch();
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
    registerModule,
    getModule,
    notify,
    notifySuccess,
    notifyError,
    notifyWarning,
    notifyInfo,
    on,
    off,
    emit,
    apiRequest,
    setButtonLoading,
    navigateTo,
    clearSearch
  };
})();

// Export for use in other modules
if (typeof window !== 'undefined') {
  window.SettingsController = SettingsController;
}

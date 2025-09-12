/**
 * Logging utility for the application
 */

'use strict';

class Logger {
  static LOG_LEVELS = {
    ERROR: 0,
    WARN: 1,
    INFO: 2,
    DEBUG: 3
  };

  constructor(level = Logger.LOG_LEVELS.INFO) {
    this.level = level;
  }

  /**
   * Log an error message
   * @param {string} message - Error message
   * @param {...any} args - Additional arguments
   */
  static error(message, ...args) {
    if (window.Logger?.level >= Logger.LOG_LEVELS.ERROR) {
      console.error(`[ERROR] ${message}`, ...args);
    }
  }

  /**
   * Log a warning message
   * @param {string} message - Warning message
   * @param {...any} args - Additional arguments
   */
  static warn(message, ...args) {
    if (window.Logger?.level >= Logger.LOG_LEVELS.WARN) {
      console.warn(`[WARN] ${message}`, ...args);
    }
  }

  /**
   * Log an info message
   * @param {string} message - Info message
   * @param {...any} args - Additional arguments
   */
  static info(message, ...args) {
    if (window.Logger?.level >= Logger.LOG_LEVELS.INFO) {
      console.info(`[INFO] ${message}`, ...args);
    }
  }

  /**
   * Log a debug message
   * @param {string} message - Debug message
   * @param {...any} args - Additional arguments
   */
  static debug(message, ...args) {
    if (window.Logger?.level >= Logger.LOG_LEVELS.DEBUG) {
      console.debug(`[DEBUG] ${message}`, ...args);
    }
  }
}

// Create global logger instance
window.Logger = new Logger(Logger.LOG_LEVELS.INFO);

// Export for module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = Logger;
}